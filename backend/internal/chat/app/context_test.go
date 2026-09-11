package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

func agentWith(prompt string) *domain.Agent {
	return &domain.Agent{ID: uuid.New(), SystemPrompt: prompt}
}

func userTurn(content string) domain.Message {
	return domain.Message{ID: uuid.New(), Role: domain.RoleUser, Content: content}
}

func assistantTurnMsg(content string) domain.Message {
	return domain.Message{ID: uuid.New(), Role: domain.RoleAssistant, Content: content}
}

// wire renders the built messages as "role:content" pairs, which is the
// shape assertions read best.
func wire(built BuiltContext) []string {
	out := make([]string, 0, len(built.Messages))
	for _, m := range built.Messages {
		out = append(out, m.Role+":"+m.Content)
	}
	return out
}

func wantWire(t *testing.T, built BuiltContext, want ...string) {
	t.Helper()
	got := wire(built)
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("wire =\n  %v\nwant\n  %v", got, want)
	}
}

func mustBlock(t *testing.T, r domain.ContextReport, kind domain.BlockKind) domain.ContextBlock {
	t.Helper()
	b, ok := r.Block(kind)
	if !ok {
		t.Fatalf("report has no %s block: %+v", kind, r.Blocks)
	}
	return b
}

func wantNoBlock(t *testing.T, r domain.ContextReport, kind domain.BlockKind) {
	t.Helper()
	if b, ok := r.Block(kind); ok {
		t.Fatalf("report has an unexpected %s block: %+v", kind, b)
	}
}

/* ── composition ─────────────────────────────────────────────────────── */

// TestBuildContextComposition carries over, unchanged in expectation, the
// four cases that used to test buildWireMessages directly. The helper was
// absorbed into the builder; the contract it encoded was not touched.
func TestBuildContextComposition(t *testing.T) {
	history := []domain.Message{
		userTurn("oi"),
		assistantTurnMsg("olá"),
		userTurn("e aí"),
	}
	current := &history[2]

	t.Run("prepends the system prompt", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent: agentWith("você é o agente do João"), History: history, Current: current,
		})
		wantWire(t, built,
			"system:você é o agente do João",
			"user:oi",
			"assistant:olá",
			"user:e aí",
		)
	})

	t.Run("blank system prompt is omitted entirely", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent: agentWith("   \n "), History: history, Current: current,
		})
		wantWire(t, built, "user:oi", "assistant:olá", "user:e aí")
		// Omitted, not empty: no system message and no instructions block.
		wantNoBlock(t, built.Report, domain.BlockInstructions)
	})

	t.Run("drops empty assistant turns", func(t *testing.T) {
		aborted := assistantTurnMsg("")
		aborted.FinishReason = string(domain.FinishAborted)
		withAborted := []domain.Message{userTurn("pergunta"), aborted, userTurn("de novo")}

		built := BuildContext(ContextInput{
			Agent: agentWith(""), History: withAborted, Current: &withAborted[2],
		})
		wantWire(t, built, "user:pergunta", "user:de novo")

		// Dropping it is not silent: the report says so.
		h := mustBlock(t, built.Report, domain.BlockHistory)
		if len(h.Exclusions) != 1 || h.Exclusions[0].Reason != domain.ReasonEmptyTurn || h.Exclusions[0].Items != 1 {
			t.Fatalf("history exclusions = %+v, want one empty_turn", h.Exclusions)
		}
	})

	t.Run("empty history with a prompt yields just the prompt", func(t *testing.T) {
		built := BuildContext(ContextInput{Agent: agentWith("sistema")})
		wantWire(t, built, "system:sistema")
		wantNoBlock(t, built.Report, domain.BlockHistory)
		wantNoBlock(t, built.Report, domain.BlockCurrentMessage)
	})

	t.Run("no agent at all composes only the conversation", func(t *testing.T) {
		built := BuildContext(ContextInput{History: history, Current: current})
		wantWire(t, built, "user:oi", "assistant:olá", "user:e aí")
	})
}

// TestBuildContextOrder pins the order the blocks are composed in. This is
// the invariant a careless refactor breaks first, and the one the model is
// most sensitive to: instructions have to arrive before anything they are
// meant to govern, and the question has to be last.
func TestBuildContextOrder(t *testing.T) {
	history := []domain.Message{
		userTurn("primeira"),
		assistantTurnMsg("resposta"),
		userTurn("atual"),
	}
	built := BuildContext(ContextInput{
		Agent: agentWith("instruções"), History: history, Current: &history[2],
	})

	wantWire(t, built,
		"system:instruções",
		"user:primeira",
		"assistant:resposta",
		"user:atual",
	)

	if built.Messages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", built.Messages[0].Role)
	}
	last := built.Messages[len(built.Messages)-1]
	if last.Role != "user" || last.Content != "atual" {
		t.Fatalf("last message = %+v, want the question being answered", last)
	}

	// Report order follows composition order.
	var kinds []string
	for _, b := range built.Report.Blocks {
		kinds = append(kinds, string(b.Kind))
	}
	if strings.Join(kinds, ",") != "instructions,history,current_message" {
		t.Fatalf("report block order = %v", kinds)
	}
}

/* ── the window ──────────────────────────────────────────────────────── */

// TestBuildContextComposesTheWindowItIsGiven documents the division of
// labour around history_limit: the repository decides how much history a
// turn replays (ORDER BY seq DESC LIMIT n), and the builder composes
// exactly that, without a second opinion. The limit itself is exercised
// end to end by TestHistoryLimitBoundsTheReplay.
func TestBuildContextComposesTheWindowItIsGiven(t *testing.T) {
	for _, size := range []int{1, 2, 7, 40} {
		history := make([]domain.Message, 0, size)
		for i := 0; i < size; i++ {
			if i%2 == 0 {
				history = append(history, userTurn("pergunta"))
			} else {
				history = append(history, assistantTurnMsg("resposta"))
			}
		}
		built := BuildContext(ContextInput{
			Agent:   agentWith(""),
			History: history,
			Current: &history[len(history)-1],
		})
		if len(built.Messages) != size {
			t.Fatalf("window of %d produced %d messages", size, len(built.Messages))
		}
	}
}

/* ── reasoning ───────────────────────────────────────────────────────── */

// TestReasoningIsNeverReplayed is the unit-level guard for the rule the
// mutation check proved is worth guarding. Reasoning is stored on
// the message and must never reach the provider on a later turn.
func TestReasoningIsNeverReplayed(t *testing.T) {
	answered := assistantTurnMsg("a resposta")
	answered.Reasoning = "o raciocínio que não pode voltar"
	answered.ReasoningMS = 4200

	history := []domain.Message{userTurn("pergunta"), answered, userTurn("de novo")}
	built := BuildContext(ContextInput{
		Agent: agentWith("instruções"), History: history, Current: &history[2],
	})

	for _, m := range built.Messages {
		if strings.Contains(m.Content, "raciocínio") {
			t.Fatalf("reasoning reached the wire: %+v", m)
		}
	}
	wantWire(t, built,
		"system:instruções",
		"user:pergunta",
		"assistant:a resposta",
		"user:de novo",
	)

	// It must not be counted either: a character budget that includes text
	// nobody sends is a budget that lies.
	h := mustBlock(t, built.Report, domain.BlockHistory)
	if h.Characters != len("pergunta")+len("a resposta") {
		t.Fatalf("history characters = %d, want only the replayed content", h.Characters)
	}
}

/* ── the current message ─────────────────────────────────────────────── */

// TestCurrentMessageIsReportedApart: the question being answered is the
// last entry of the window, and the report tells it apart from the thread
// behind it without duplicating it on the wire.
func TestCurrentMessageIsReportedApart(t *testing.T) {
	history := []domain.Message{
		userTurn("antiga"),
		assistantTurnMsg("resposta antiga"),
		userTurn("a pergunta de agora"),
	}
	current := &history[2]

	built := BuildContext(ContextInput{
		Agent: agentWith("instruções"), History: history, Current: current,
	})

	if got := len(built.Messages); got != 4 {
		t.Fatalf("messages = %d, want 4; the question must not be sent twice", got)
	}

	cur := mustBlock(t, built.Report, domain.BlockCurrentMessage)
	if cur.Items != 1 {
		t.Fatalf("current_message items = %d, want 1", cur.Items)
	}
	if cur.Characters != len([]rune("a pergunta de agora")) {
		t.Fatalf("current_message characters = %d", cur.Characters)
	}
	h := mustBlock(t, built.Report, domain.BlockHistory)
	if h.Items != 2 {
		t.Fatalf("history items = %d, want the two turns before the question", h.Items)
	}

	t.Run("without a current message everything is history", func(t *testing.T) {
		built := BuildContext(ContextInput{Agent: agentWith(""), History: history})
		wantNoBlock(t, built.Report, domain.BlockCurrentMessage)
		if h := mustBlock(t, built.Report, domain.BlockHistory); h.Items != 3 {
			t.Fatalf("history items = %d, want 3", h.Items)
		}
	})
}

/* ── the report ──────────────────────────────────────────────────────── */

// TestContextReportMatchesMessages: the report is only useful if it
// describes the payload that was actually built. Anything that appends a
// message without accounting for it, or accounts for something it did not
// append, fails here.
func TestContextReportMatchesMessages(t *testing.T) {
	aborted := assistantTurnMsg("")
	history := []domain.Message{
		userTurn("uma"),
		aborted,
		assistantTurnMsg("duas"),
		userTurn("três"),
	}
	built := BuildContext(ContextInput{
		Agent: agentWith("sistema"), History: history, Current: &history[3],
	})

	items, characters := 0, 0
	for _, b := range built.Report.Blocks {
		items += b.Items
		characters += b.Characters
		if b.EstimatedTokens != EstimateTokens(b.Characters) {
			t.Fatalf("block %s: estimate %d does not match its characters %d",
				b.Kind, b.EstimatedTokens, b.Characters)
		}
	}
	if items != len(built.Messages) {
		t.Fatalf("report accounts for %d items, wire carries %d", items, len(built.Messages))
	}

	wantChars := 0
	for _, m := range built.Messages {
		wantChars += len([]rune(m.Content))
	}
	if characters != wantChars || built.Report.TotalCharacters != wantChars {
		t.Fatalf("characters: blocks %d, total %d, wire %d",
			characters, built.Report.TotalCharacters, wantChars)
	}

	// The total is the sum of the block estimates, so a reader adding up
	// the rows on screen gets the number the report shows.
	sum := 0
	for _, b := range built.Report.Blocks {
		sum += b.EstimatedTokens
	}
	if built.Report.TotalEstimatedTokens != sum {
		t.Fatalf("total estimate %d, sum of blocks %d", built.Report.TotalEstimatedTokens, sum)
	}
}

// TestContextReportCountsRunes: characters are what the model reads, not
// what the disk stores. Portuguese makes the difference visible.
func TestContextReportCountsRunes(t *testing.T) {
	const question = "coração, ação e não"
	runes := len([]rune(question))
	bytes := len(question)
	if runes == bytes {
		t.Fatal("fixture is not multi-byte; the test would prove nothing")
	}

	history := []domain.Message{userTurn(question)}
	built := BuildContext(ContextInput{
		Agent: agentWith(""), History: history, Current: &history[0],
	})

	if got := built.Report.TotalCharacters; got != runes {
		t.Fatalf("characters = %d, want %d runes (not %d bytes)", got, runes, bytes)
	}
	// And the content itself survives intact.
	if built.Messages[0].Content != question {
		t.Fatalf("content = %q", built.Messages[0].Content)
	}
}

// TestAuxiliaryBlocksAreAbsentWhenEmpty is what protects the turn of an
// agent that has neither memories nor sources: byte for byte, it must send
// what it sent before either capability existed.
//
// This test carried a different name in each of the last two batches
// ("memory and sources have no producer", then "sources have no producer").
// Both kinds have a producer now, and what has to stay true is not that
// they are unbuilt — it is that building them changed nothing for an agent
// that uses neither.
func TestAuxiliaryBlocksAreAbsentWhenEmpty(t *testing.T) {
	history := []domain.Message{userTurn("pergunta")}
	built := BuildContext(ContextInput{
		Agent: agentWith("instruções"), History: history, Current: &history[0],
	})
	wantWire(t, built, "system:instruções", "user:pergunta")
	wantNoBlock(t, built.Report, domain.BlockMemory)
	wantNoBlock(t, built.Report, domain.BlockSources)
}

/* ── memory ──────────────────────────────────────────────────────────── */

func memoryWith(content string) domain.Memory {
	return domain.Memory{ID: uuid.New(), Content: content, Enabled: true}
}

// TestMemoryBlock covers what the block does on the wire: one message
// whatever the count, in the fixed position, and absent entirely when there
// is nothing to say.
func TestMemoryBlock(t *testing.T) {
	history := []domain.Message{userTurn("e aí")}

	t.Run("no memories contribute nothing", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent: agentWith("instruções"), History: history, Current: &history[0],
		})
		wantWire(t, built, "system:instruções", "user:e aí")
		wantNoBlock(t, built.Report, domain.BlockMemory)
	})

	t.Run("one system message whatever the count", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent: agentWith("instruções"),
			Memories: []domain.Memory{
				memoryWith("Agents vem antes de Finance"),
				memoryWith("responder em português"),
			},
			History: history, Current: &history[0],
		})
		wantWire(t, built,
			"system:instruções",
			"system:"+memoryHeader+"\n- Agents vem antes de Finance\n- responder em português",
			"user:e aí",
		)
		b := mustBlock(t, built.Report, domain.BlockMemory)
		if b.Items != 1 {
			t.Fatalf("memory block items = %d, want 1 message carrying both facts", b.Items)
		}
	})

	t.Run("sits between instructions and the conversation", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent:    agentWith("instruções"),
			Memories: []domain.Memory{memoryWith("um fato")},
			History:  history, Current: &history[0],
		})
		kinds := make([]domain.BlockKind, 0, len(built.Report.Blocks))
		for _, b := range built.Report.Blocks {
			kinds = append(kinds, b.Kind)
		}
		want := []domain.BlockKind{domain.BlockInstructions, domain.BlockMemory, domain.BlockCurrentMessage}
		if len(kinds) != len(want) {
			t.Fatalf("blocks = %v, want %v", kinds, want)
		}
		for i := range want {
			if kinds[i] != want[i] {
				t.Fatalf("blocks = %v, want %v", kinds, want)
			}
		}
	})

	t.Run("a memory with line breaks stays on one bullet", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent:    agentWith(""),
			Memories: []domain.Memory{memoryWith("linha um\nlinha dois")},
			History:  history, Current: &history[0],
		})
		got := built.Messages[0].Content
		if strings.Count(got, "\n") != 1 {
			t.Fatalf("block = %q, want exactly one newline (the bullet)", got)
		}
	})
}

// TestSelectMemories is the budget rule, on its own.
//
// The two properties that matter: an item enters whole or not at all, and a
// long item being skipped does not hide the short ones behind it. The second
// is why the loop continues instead of breaking — order is a priority, not
// a queue.
func TestSelectMemories(t *testing.T) {
	// Budget is measured against the rendered block, header included.
	overhead := len([]rune(memoryHeader))
	bullet := len([]rune(memoryBullet))

	t.Run("everything fits", func(t *testing.T) {
		in := []domain.Memory{memoryWith("aaa"), memoryWith("bbb")}
		selected, dropped := SelectMemories(in, 1000)
		if len(selected) != 2 || len(dropped) != 0 {
			t.Fatalf("selected %d, dropped %d; want 2 and 0", len(selected), len(dropped))
		}
	})

	t.Run("cuts at the item, never inside it", func(t *testing.T) {
		// Room for the header plus exactly one three-character bullet.
		budget := overhead + bullet + 3
		in := []domain.Memory{memoryWith("aaa"), memoryWith("bbb")}
		selected, dropped := SelectMemories(in, budget)
		if len(selected) != 1 || selected[0].Content != "aaa" {
			t.Fatalf("selected = %+v, want only the first", selected)
		}
		if len(dropped) != 1 || dropped[0].Content != "bbb" {
			t.Fatalf("dropped = %+v, want only the second", dropped)
		}
	})

	t.Run("a skipped item does not block the ones behind it", func(t *testing.T) {
		budget := overhead + 2*(bullet+3)
		in := []domain.Memory{
			memoryWith("aaa"),
			memoryWith(strings.Repeat("x", 500)),
			memoryWith("ccc"),
		}
		selected, dropped := SelectMemories(in, budget)
		if len(selected) != 2 || selected[0].Content != "aaa" || selected[1].Content != "ccc" {
			t.Fatalf("selected = %d items, want aaa and ccc", len(selected))
		}
		if len(dropped) != 1 {
			t.Fatalf("dropped = %d, want the long one only", len(dropped))
		}
	})

	t.Run("nothing fits", func(t *testing.T) {
		in := []domain.Memory{memoryWith("aaa")}
		selected, dropped := SelectMemories(in, 1)
		if len(selected) != 0 || len(dropped) != 1 {
			t.Fatalf("selected %d, dropped %d; want 0 and 1", len(selected), len(dropped))
		}
	})
}

// TestMemoryBudgetMatchesRenderedBlock is the identity that keeps the budget
// honest: what SelectMemories charged for a slice is exactly what
// RenderMemoryBlock produces for it.
//
// Without this, the two could drift and the ceiling would be a number that
// describes nothing. The accented content is deliberate — a byte count would
// fail here, and the whole point is that characters are what is measured.
func TestMemoryBudgetMatchesRenderedBlock(t *testing.T) {
	in := []domain.Memory{
		memoryWith("acentuação é contada em caracteres, não em bytes"),
		memoryWith("três"),
		memoryWith(strings.Repeat("z", 200)),
	}
	selected, _ := SelectMemories(in, MemoryBudgetChars)
	if len(selected) != 3 {
		t.Fatalf("selected = %d, want all three inside the default budget", len(selected))
	}
	rendered := len([]rune(RenderMemoryBlock(selected)))

	// Recompute the charge the same way the selection did, from the outside.
	charged := len([]rune(memoryHeader))
	for _, m := range selected {
		charged += len([]rune(memoryBullet)) + len([]rune(m.Content))
	}
	if rendered != charged {
		t.Fatalf("rendered %d characters, budget charged %d", rendered, charged)
	}
}

// TestMemoryBudgetCutIsReported: cutting is normal, cutting silently is not.
func TestMemoryBudgetCutIsReported(t *testing.T) {
	history := []domain.Message{userTurn("pergunta")}
	long := strings.Repeat("m", MemoryBudgetChars)
	built := BuildContext(ContextInput{
		Agent: agentWith(""),
		Memories: []domain.Memory{
			memoryWith("cabe"),
			memoryWith(long),
		},
		History: history, Current: &history[0],
	})

	b := mustBlock(t, built.Report, domain.BlockMemory)
	if b.Items != 1 {
		t.Fatalf("memory items = %d, want the one message that fit", b.Items)
	}
	if len(b.Exclusions) != 1 {
		t.Fatalf("exclusions = %+v, want one", b.Exclusions)
	}
	ex := b.Exclusions[0]
	if ex.Reason != domain.ReasonBudget {
		t.Fatalf("reason = %q, want %q", ex.Reason, domain.ReasonBudget)
	}
	if ex.Items != 1 || ex.Characters != MemoryBudgetChars {
		t.Fatalf("exclusion = %+v, want 1 item of %d characters", ex, MemoryBudgetChars)
	}
	if strings.Contains(built.Messages[0].Content, long) {
		t.Fatalf("the dropped memory reached the wire")
	}
}

// TestMemoryUnavailableIsReported: a failed read degrades the turn and says
// so. The turn still happens — losing an answer because a side table was
// unreachable is the worse trade.
func TestMemoryUnavailableIsReported(t *testing.T) {
	history := []domain.Message{userTurn("pergunta")}
	built := BuildContext(ContextInput{
		Agent:             agentWith("instruções"),
		MemoryUnavailable: true,
		History:           history, Current: &history[0],
	})

	wantWire(t, built, "system:instruções", "user:pergunta")

	b := mustBlock(t, built.Report, domain.BlockMemory)
	if b.Items != 0 || b.Characters != 0 {
		t.Fatalf("memory block = %+v, want nothing on the wire", b)
	}
	if len(b.Exclusions) != 1 || b.Exclusions[0].Reason != domain.ReasonUnavailable {
		t.Fatalf("exclusions = %+v, want one %q", b.Exclusions, domain.ReasonUnavailable)
	}
}

/* ── sources ─────────────────────────────────────────────────────────── */

func sourceWith(title, content string) domain.Source {
	return domain.Source{ID: uuid.New(), Title: title, Content: content, Enabled: true}
}

// TestSourcesBlock covers what the block puts on the wire.
func TestSourcesBlock(t *testing.T) {
	history := []domain.Message{userTurn("e aí")}

	t.Run("no sources contribute nothing", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent: agentWith("instruções"), History: history, Current: &history[0],
		})
		wantNoBlock(t, built.Report, domain.BlockSources)
	})

	t.Run("one system message, each source under its title", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent: agentWith("instruções"),
			Sources: []domain.Source{
				sourceWith("Estratégia", "publicar às terças"),
				sourceWith("Arquitetura", "módulo não importa módulo"),
			},
			History: history, Current: &history[0],
		})
		wantWire(t, built,
			"system:instruções",
			"system:"+sourcesHeader+
				"\n\n## Estratégia\npublicar às terças"+
				"\n\n## Arquitetura\nmódulo não importa módulo",
			"user:e aí",
		)
		b := mustBlock(t, built.Report, domain.BlockSources)
		if b.Items != 1 {
			t.Fatalf("sources block items = %d, want 1 message carrying both documents", b.Items)
		}
	})

	t.Run("the description never reaches the model", func(t *testing.T) {
		src := sourceWith("Título", "corpo do documento")
		src.Description = "nota que o usuário escreveu para si mesmo"
		built := BuildContext(ContextInput{
			Agent: agentWith(""), Sources: []domain.Source{src},
			History: history, Current: &history[0],
		})
		if strings.Contains(built.Messages[0].Content, "nota que o usuário") {
			t.Fatalf("the description was sent to the model: %q", built.Messages[0].Content)
		}
	})

	t.Run("paragraphs survive: a source is a document, not a bullet", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent:   agentWith(""),
			Sources: []domain.Source{sourceWith("Doc", "primeiro parágrafo\n\nsegundo parágrafo")},
			History: history, Current: &history[0],
		})
		if !strings.Contains(built.Messages[0].Content, "primeiro parágrafo\n\nsegundo parágrafo") {
			t.Fatalf("the document was flattened: %q", built.Messages[0].Content)
		}
	})

	t.Run("blocks are composed in the fixed order", func(t *testing.T) {
		built := BuildContext(ContextInput{
			Agent:    agentWith("instruções"),
			Memories: []domain.Memory{memoryWith("um fato")},
			Sources:  []domain.Source{sourceWith("Doc", "corpo")},
			History:  history, Current: &history[0],
		})
		var kinds []domain.BlockKind
		for _, b := range built.Report.Blocks {
			kinds = append(kinds, b.Kind)
		}
		want := []domain.BlockKind{domain.BlockInstructions, domain.BlockMemory, domain.BlockSources, domain.BlockCurrentMessage}
		if fmt.Sprint(kinds) != fmt.Sprint(want) {
			t.Fatalf("blocks = %v, want %v", kinds, want)
		}
	})
}

// TestSourcesBudgetMatchesRenderedBlock is the identity that keeps the
// budget honest: what SelectSources charged for a slice is exactly what
// RenderSourcesBlock produces for it.
//
// Accented titles and content are deliberate — a byte count fails here.
func TestSourcesBudgetMatchesRenderedBlock(t *testing.T) {
	in := []domain.Source{
		sourceWith("Estratégia de conteúdo", "cadência, formatos e pilares do canal"),
		sourceWith("Arquitetura", strings.Repeat("é", 300)),
	}
	selected, dropped := SelectSources(in, SourcesBudgetChars)
	if len(selected) != 2 || len(dropped) != 0 {
		t.Fatalf("selected %d, dropped %d; want both inside the default budget",
			len(selected), len(dropped))
	}
	rendered := len([]rune(RenderSourcesBlock(selected)))

	charged := len([]rune(sourcesHeader))
	for _, s := range selected {
		charged += sourceCost(s)
	}
	if rendered != charged {
		t.Fatalf("rendered %d characters, budget charged %d", rendered, charged)
	}
}

// TestSelectSourcesIsWholeDocumentsOnly is the rule that cannot be broken.
//
// Handing the model half a document is worse than handing it none: it will
// answer about the half it received, with confidence, and nothing in the
// answer will say a page was missing.
func TestSelectSourcesIsWholeDocumentsOnly(t *testing.T) {
	overhead := len([]rune(sourcesHeader))

	t.Run("a source that does not fit is dropped entire", func(t *testing.T) {
		small := sourceWith("Pequena", strings.Repeat("a", 100))
		big := sourceWith("Grande", strings.Repeat("b", 5000))
		budget := overhead + sourceCost(small) + 10

		selected, dropped := SelectSources([]domain.Source{small, big}, budget)
		if len(selected) != 1 || selected[0].Title != "Pequena" {
			t.Fatalf("selected = %+v, want only the small one", titlesOf(selected))
		}
		if len(dropped) != 1 || dropped[0].Title != "Grande" {
			t.Fatalf("dropped = %v, want only the big one", titlesOf(dropped))
		}
		// And nothing of the dropped document leaked into the block.
		if strings.Contains(RenderSourcesBlock(selected), "b") {
			t.Fatalf("part of the dropped source reached the block")
		}
	})

	t.Run("a source too big to ever fit does not block the others", func(t *testing.T) {
		huge := sourceWith("Enorme", strings.Repeat("x", SourcesBudgetChars+1))
		ok1 := sourceWith("Uma", "conteúdo curto")
		ok2 := sourceWith("Outra", "também curto")

		selected, dropped := SelectSources([]domain.Source{huge, ok1, ok2}, SourcesBudgetChars)
		if len(selected) != 2 {
			t.Fatalf("selected = %v, want the two that fit", titlesOf(selected))
		}
		if len(dropped) != 1 || dropped[0].Title != "Enorme" {
			t.Fatalf("dropped = %v, want only the oversized one", titlesOf(dropped))
		}
	})

	t.Run("the block never exceeds the budget", func(t *testing.T) {
		var in []domain.Source
		for i := 0; i < 20; i++ {
			in = append(in, sourceWith("Doc", strings.Repeat("y", 1000)))
		}
		selected, _ := SelectSources(in, SourcesBudgetChars)
		if got := len([]rune(RenderSourcesBlock(selected))); got > SourcesBudgetChars {
			t.Fatalf("block is %d characters, over the %d ceiling", got, SourcesBudgetChars)
		}
	})
}

// TestSourceExceedsBudget separates the two ways a source can be absent.
//
// "Did not fit this time" is a fact about the collection and changes when
// something else is switched off. "Cannot ever fit" is a fact about the
// source itself. A user who cannot tell them apart keeps switching things
// off, waiting for an event that will never happen.
func TestSourceExceedsBudget(t *testing.T) {
	fits := sourceWith("Cabe", strings.Repeat("a", 1000))
	if SourceExceedsBudget(fits, SourcesBudgetChars) {
		t.Fatalf("a 1.000-character source was reported as impossible to fit")
	}

	never := sourceWith("Nunca", strings.Repeat("a", SourcesBudgetChars))
	if !SourceExceedsBudget(never, SourcesBudgetChars) {
		t.Fatalf("a source at exactly the budget fits only if the header is free, and it is not")
	}

	// The domain allows storing more than the block can ever carry, on
	// purpose: the source stays, and the interface says why it is not being
	// used. This asserts the two numbers really are in that relation.
	if domain.MaxSourceContent <= SourcesBudgetChars {
		t.Fatalf("MaxSourceContent (%d) no longer exceeds the budget (%d); the oversized case became unreachable",
			domain.MaxSourceContent, SourcesBudgetChars)
	}
}

// TestSourcesBudgetCutIsReported: cutting is normal, cutting silently is
// not. The exclusion is charged at what the source would have cost the
// block, because the report is an account of the budget.
//
// The two reasons are asserted apart because they call for two different
// actions from whoever reads them: `budget` means switching something else
// off would have let this in, `too_large` means nothing would have.
func TestSourcesBudgetCutIsReported(t *testing.T) {
	history := []domain.Message{userTurn("pergunta")}

	t.Run("a source that lost the race is budget", func(t *testing.T) {
		// Half the budget each: the second fits on its own, and only loses
		// because the first one got there first.
		half := SourcesBudgetChars / 2
		first := sourceWith("Primeira", strings.Repeat("a", half))
		second := sourceWith("Segunda", strings.Repeat("b", half))
		built := BuildContext(ContextInput{
			Agent:   agentWith(""),
			Sources: []domain.Source{first, second},
			History: history, Current: &history[0],
		})

		b := mustBlock(t, built.Report, domain.BlockSources)
		if len(b.Exclusions) != 1 {
			t.Fatalf("exclusions = %+v, want one", b.Exclusions)
		}
		ex := b.Exclusions[0]
		if ex.Reason != domain.ReasonBudget || ex.Items != 1 {
			t.Fatalf("exclusion = %+v, want one item excluded for budget", ex)
		}
		if ex.Characters != sourceCost(second) {
			t.Fatalf("exclusion charged %d characters, the source would have cost %d",
				ex.Characters, sourceCost(second))
		}
		// It really could have fitted alone; that is what makes `budget`
		// the right word for it.
		if SourceExceedsBudget(second, SourcesBudgetChars) {
			t.Fatalf("the fixture is wrong: the second source cannot fit alone either")
		}
	})

	t.Run("a source that could never fit is too_large", func(t *testing.T) {
		huge := sourceWith("Enorme", strings.Repeat("x", SourcesBudgetChars))
		built := BuildContext(ContextInput{
			Agent:   agentWith(""),
			Sources: []domain.Source{sourceWith("Cabe", "curto"), huge},
			History: history, Current: &history[0],
		})

		b := mustBlock(t, built.Report, domain.BlockSources)
		if b.Items != 1 {
			t.Fatalf("sources items = %d, want the one message that fit", b.Items)
		}
		if len(b.Exclusions) != 1 {
			t.Fatalf("exclusions = %+v, want one", b.Exclusions)
		}
		ex := b.Exclusions[0]
		if ex.Reason != domain.ReasonTooLarge || ex.Items != 1 {
			t.Fatalf("exclusion = %+v, want one item excluded as too_large", ex)
		}
		if ex.Characters != sourceCost(huge) {
			t.Fatalf("exclusion charged %d characters, the source would have cost %d",
				ex.Characters, sourceCost(huge))
		}
	})

	t.Run("both reasons can appear on one block, grouped apart", func(t *testing.T) {
		huge := sourceWith("Enorme", strings.Repeat("x", SourcesBudgetChars))
		half := SourcesBudgetChars / 2
		built := BuildContext(ContextInput{
			Agent: agentWith(""),
			Sources: []domain.Source{
				sourceWith("Primeira", strings.Repeat("a", half)),
				sourceWith("Segunda", strings.Repeat("b", half)),
				huge,
			},
			History: history, Current: &history[0],
		})

		b := mustBlock(t, built.Report, domain.BlockSources)
		reasons := map[domain.ExclusionReason]int{}
		for _, ex := range b.Exclusions {
			reasons[ex.Reason] = ex.Items
		}
		if reasons[domain.ReasonBudget] != 1 || reasons[domain.ReasonTooLarge] != 1 {
			t.Fatalf("exclusions = %+v, want one of each reason", b.Exclusions)
		}
	})
}

// TestSourcesUnavailableIsReported: a failed read degrades the turn and
// says so, exactly as memory does.
func TestSourcesUnavailableIsReported(t *testing.T) {
	history := []domain.Message{userTurn("pergunta")}
	built := BuildContext(ContextInput{
		Agent:              agentWith("instruções"),
		SourcesUnavailable: true,
		History:            history, Current: &history[0],
	})

	wantWire(t, built, "system:instruções", "user:pergunta")

	b := mustBlock(t, built.Report, domain.BlockSources)
	if b.Items != 0 || b.Characters != 0 {
		t.Fatalf("sources block = %+v, want nothing on the wire", b)
	}
	if len(b.Exclusions) != 1 || b.Exclusions[0].Reason != domain.ReasonUnavailable {
		t.Fatalf("exclusions = %+v, want one %q", b.Exclusions, domain.ReasonUnavailable)
	}
}

// TestSelectionOrderIsPreserved: neither selector reorders. The repository
// decides priority; the builder only decides where the line falls.
//
// A selector that sorted would silently override the ordering the query
// established, and the block the model receives would stop matching the
// list the user is looking at.
func TestSelectionOrderIsPreserved(t *testing.T) {
	in := []domain.Source{
		sourceWith("Terceira", "c"),
		sourceWith("Primeira", "a"),
		sourceWith("Segunda", "b"),
	}
	selected, _ := SelectSources(in, SourcesBudgetChars)
	if got := titlesOf(selected); fmt.Sprint(got) != fmt.Sprint([]string{"Terceira", "Primeira", "Segunda"}) {
		t.Fatalf("order = %v, want the input order untouched", got)
	}
}

func titlesOf(sources []domain.Source) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Title)
	}
	return out
}

/* ── estimation ──────────────────────────────────────────────────────── */

// TestEstimateTokens pins the model-agnostic estimator at its calibrated
// density.
//
// It used to divide characters by four. Measured against 159 real turns that
// was low by nearly a factor of two, in the one direction a number feeding a
// spending limit must never be low in — see the calibration note in
// context.go. The default is now the densest family measured (0,50), which
// is what a caller with no model in hand should be given.
func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		characters, want int
	}{
		{0, 0},
		{-5, 0}, // defensive: a negative count is a bug elsewhere, not a panic here
		{1, 1},
		{3, 2}, // rounds up: a partial token is a token, and it is billed
		{4, 2},
		{5, 3},
		{4000, 2000},
	}
	for _, tc := range cases {
		if got := EstimateTokens(tc.characters); got != tc.want {
			t.Errorf("EstimateTokens(%d) = %d, want %d", tc.characters, got, tc.want)
		}
	}
}

// TestEstimatorIsCalibratedPerModel pins the table and, more importantly,
// the two properties that make it safe.
func TestEstimatorIsCalibratedPerModel(t *testing.T) {
	// The densities measured on the real corpus, as maxima per family.
	cases := map[string]float64{
		"claude-opus-4-7":   0.50,
		"claude-opus-5":     0.50,
		"claude-sonnet-4-6": 0.40,
		"claude-haiku-4-5":  0.43,
	}
	for model, density := range cases {
		if got, want := EstimatorFor(model).Tokens(1000), int(1000*density); got != want {
			t.Errorf("EstimatorFor(%q).Tokens(1000) = %d, want %d", model, got, want)
		}
	}

	// An unmeasured model gets the MOST pessimistic answer in the table, not
	// an average of the ones that happen to have been measured. An unknown
	// model is where being wrong is most likely, so it is where the estimate
	// must lean hardest away from optimism.
	unknown := EstimatorFor("some-model-nobody-measured").Tokens(1000)
	for model := range cases {
		if EstimatorFor(model).Tokens(1000) > unknown {
			t.Fatalf("model %q estimates higher than the unknown-model default; "+
				"the default is not the pessimistic one", model)
		}
	}

	// The zero value must not estimate zero. A TokenEstimator that was never
	// initialised would otherwise report every prompt as free, which a
	// budget would read as unlimited headroom.
	var zero TokenEstimator
	if got := zero.Tokens(1000); got != unknown {
		t.Fatalf("the zero TokenEstimator gave %d for 1000 characters, want the "+
			"pessimistic default %d", got, unknown)
	}
}

// TestCalibratedEstimateBeatsTheOldHeuristicOnRealTurns is the corpus test.
//
// Each row is a real turn from the audit: the report's own character count
// against the prompt tokens the gateway actually billed. The claim is not
// that the estimator is exact — it is named an estimate — but that it is no
// longer LOW, which is the property a budget depends on.
func TestCalibratedEstimateBeatsTheOldHeuristicOnRealTurns(t *testing.T) {
	// model, characters, tokens the provider reported.
	corpus := []struct {
		model      string
		characters int
		actual     int
	}{
		{"claude-opus-4-7", 83106, 41348},
		{"claude-opus-4-7", 86106, 42913},
		{"claude-opus-4-7", 101019, 50190},
		{"claude-opus-4-7", 128884, 61277},
		{"claude-opus-4-7", 33751, 12737},
		{"claude-haiku-4-5", 5686, 2102},
		{"claude-haiku-4-5", 8629, 2881},
		{"claude-sonnet-4-6", 10274, 3636},
	}

	oldLow, newLow := 0, 0
	for _, c := range corpus {
		old := (c.characters + 3) / 4 // the retired chars/4 heuristic
		got := EstimatorFor(c.model).Tokens(c.characters)
		if old < c.actual {
			oldLow++
		}
		if got < c.actual {
			newLow++
		}
		if got < c.actual {
			t.Errorf("%s: estimated %d for %d characters, provider billed %d — "+
				"the estimate is LOW, which is the direction a budget cannot "+
				"tolerate", c.model, got, c.characters, c.actual)
		}
	}
	if oldLow != len(corpus) {
		t.Fatalf("the old heuristic understated %d of %d real turns; the fixture no "+
			"longer reproduces the problem this calibration exists to fix",
			oldLow, len(corpus))
	}
	t.Logf("old heuristic understated %d/%d real turns; calibrated estimator "+
		"understates %d/%d", oldLow, len(corpus), newLow, len(corpus))
}

/* ── the message type on the wire ────────────────────────────────────── */

// TestBuiltMessagesAreWireMessages guards against the builder growing its
// own message type. The provider adapter consumes ports.ChatMessage, and
// the builder must keep speaking it.
func TestBuiltMessagesAreWireMessages(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{Agent: agentWith("p"), History: history, Current: &history[0]})
	var _ []ports.ChatMessage = built.Messages
	if built.Messages[0].Role != "system" {
		t.Fatalf("unexpected first message: %+v", built.Messages[0])
	}
}

/* ── tools ───────────────────────────────────────────────────────────── */

func toolDef(name string) domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        domain.ToolName(name),
		Title:       "T",
		Description: "does a thing",
		Effect:      domain.EffectRead,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{"text": {Type: domain.TypeString}},
			Required:   []string{"text"},
		},
	}
}

// The declaration is accounted for and is NOT a message. It rides in the
// request's own `tools` field, so a builder that appended it would send it
// twice and a builder that ignored it would understate the turn.
//
// ── What the message count means since the grounding policy ────────────
// A turn with capabilities now carries exactly one message the turn without
// them does not, and it is the policy — not the declaration. The assertion
// is written as "one more, and it is the policy" rather than "the same
// number", because the property being protected is that the SCHEMAS never
// become messages, and a bare count can no longer say that on its own.
func TestToolsAreCountedButNotAppended(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{
		Agent:   agentWith("prompt"),
		Tools:   []domain.ToolDefinition{toolDef("system.echo")},
		History: history,
		Current: &history[0],
	})

	withoutTools := BuildContext(ContextInput{
		Agent: agentWith("prompt"), History: history, Current: &history[0],
	})
	if got := len(built.Messages) - len(withoutTools.Messages); got != 1 {
		t.Fatalf("declaring a tool added %d messages; it must add exactly one, the policy", got)
	}
	// And the one it added is the policy, not a schema in prose.
	var extra string
	for _, m := range built.Messages {
		if m.Role == "system" && !containsAny(withoutTools.Messages, m.Content) {
			extra = m.Content
		}
	}
	if extra != groundingPolicy {
		t.Fatalf("the extra message is not the grounding policy: %q", extra)
	}
	for _, m := range built.Messages {
		if strings.Contains(m.Content, "system.echo") {
			t.Fatalf("a tool name reached the message list: %q", m.Content)
		}
	}
	if len(built.Tools) != 1 {
		t.Fatalf("built.Tools = %+v, want the declaration carried separately", built.Tools)
	}

	block, ok := built.Report.Block(domain.BlockTools)
	if !ok {
		t.Fatal("the tool declaration was not reported; it is paid for on every call")
	}
	if block.Items != 1 || block.Characters <= 0 || block.EstimatedTokens <= 0 {
		t.Fatalf("tools block = %+v", block)
	}
	if built.Report.TotalCharacters <= withoutTools.Report.TotalCharacters {
		t.Fatal("the total did not grow with the declaration")
	}
}

// An agent with no tools reports no tools block at all, rather than one
// reading zero. Absent is how this report says "contributed nothing".
func TestNoToolsMeansNoToolsBlock(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{Agent: agentWith("p"), History: history, Current: &history[0]})
	if _, ok := built.Report.Block(domain.BlockTools); ok {
		t.Fatal("a tools block was reported for an agent with none")
	}
	if built.Tools != nil {
		t.Fatalf("built.Tools = %+v, want nil", built.Tools)
	}
}

// The declaration sits between the instructions and memory. The order is
// the order a reader scans the report, and it is fixed.
func TestToolsAreReportedAfterInstructions(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{
		Agent:    agentWith("prompt"),
		Tools:    []domain.ToolDefinition{toolDef("system.echo")},
		Memories: []domain.Memory{{Content: "lembre disto"}},
		History:  history,
		Current:  &history[0],
	})

	var order []domain.BlockKind
	for _, b := range built.Report.Blocks {
		order = append(order, b.Kind)
	}
	// No history block: the window's only entry IS the current message, so
	// the split reports it once, as the question rather than as the thread.
	want := []domain.BlockKind{
		domain.BlockInstructions, domain.BlockTools, domain.BlockMemory,
		domain.BlockCurrentMessage,
	}
	if len(order) != len(want) {
		t.Fatalf("block order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("block order = %v, want %v", order, want)
		}
	}
}

// addToBlock is how the tool exchange reaches a report the builder already
// finished. It must keep the totals equal to the sum of the blocks, which
// is the property every number on screen depends on.
func TestAddToBlockKeepsTotalsConsistent(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{Agent: agentWith("p"), History: history, Current: &history[0]})
	report := built.Report

	addToBlock(&report, EstimatorFor(""), domain.BlockToolResults, 2, 400)

	block, ok := report.Block(domain.BlockToolResults)
	if !ok || block.Items != 2 || block.Characters != 400 {
		t.Fatalf("tool results block = %+v", block)
	}
	sum, tokens := 0, 0
	for _, b := range report.Blocks {
		sum += b.Characters
		tokens += b.EstimatedTokens
	}
	if sum != report.TotalCharacters || tokens != report.TotalEstimatedTokens {
		t.Fatalf("totals = %d/%d, blocks add up to %d/%d",
			report.TotalCharacters, report.TotalEstimatedTokens, sum, tokens)
	}

	// A second exchange accumulates into the same block rather than adding a
	// second one: a turn has one tool exchange, however many rounds it took.
	addToBlock(&report, EstimatorFor(""), domain.BlockToolResults, 1, 100)
	block, _ = report.Block(domain.BlockToolResults)
	if block.Items != 3 || block.Characters != 500 {
		t.Fatalf("after a second round: %+v", block)
	}
}

// containsAny reports whether any message already carries this content.
func containsAny(msgs []ports.ChatMessage, content string) bool {
	for _, m := range msgs {
		if m.Content == content {
			return true
		}
	}
	return false
}

/* ── the grounding policy ────────────────────────────────────────────── */

// The policy reaches the model exactly when the turn can act on it.
//
// This is the whole gate: a turn with capabilities is told how to use them,
// and a turn without any is charged nothing for advice it cannot follow.
func TestGroundingPolicyIsSentOnlyWhenThereAreCapabilities(t *testing.T) {
	history := []domain.Message{userTurn("oi")}

	with := BuildContext(ContextInput{
		Agent:   agentWith("prompt"),
		Tools:   []domain.ToolDefinition{toolDef("system.echo")},
		History: history, Current: &history[0],
	})
	if !containsAny(with.Messages, groundingPolicy) {
		t.Fatal("a turn with capabilities was not told how to use them")
	}

	without := BuildContext(ContextInput{
		Agent: agentWith("prompt"), History: history, Current: &history[0],
	})
	if containsAny(without.Messages, groundingPolicy) {
		t.Fatal("a turn with no capabilities was charged for a policy it cannot follow")
	}
}

// An agent that never wrote a system prompt is the agent this policy was
// reported against: it used to receive no instructions whatsoever.
func TestAnAgentWithNoPromptStillGetsThePolicy(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{
		Agent:   agentWith(""),
		Tools:   []domain.ToolDefinition{toolDef("system.echo")},
		History: history, Current: &history[0],
	})

	if !containsAny(built.Messages, groundingPolicy) {
		t.Fatal("an agent with an empty prompt received no instructions at all")
	}
	// And an empty prompt still contributes no message of its own.
	systems := 0
	for _, m := range built.Messages {
		if m.Role == "system" {
			systems++
		}
	}
	if systems != 1 {
		t.Fatalf("system messages = %d, want only the policy", systems)
	}
}

// The policy is stated before the agent's own voice, so an agent can
// specialise on top of a rule rather than have one appended to it.
func TestThePolicyPrecedesTheAgentsOwnPrompt(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{
		Agent:   agentWith("você é o agente de testes"),
		Tools:   []domain.ToolDefinition{toolDef("system.echo")},
		History: history, Current: &history[0],
	})

	if built.Messages[0].Content != groundingPolicy {
		t.Fatalf("first message = %q, want the policy", built.Messages[0].Content)
	}
	if built.Messages[1].Content != "você é o agente de testes" {
		t.Fatalf("second message = %q, want the agent's prompt unchanged",
			built.Messages[1].Content)
	}
}

// The policy is about the relationship between a claim and its evidence. It
// must survive the arrival of every future integration without an edit, so
// it may not name one — nor any tool, provider or vendor.
func TestThePolicyNamesNothingSpecific(t *testing.T) {
	lower := strings.ToLower(groundingPolicy)
	for _, forbidden := range []string{
		"github", "slack", "gmail", "litellm", "openai", "anthropic", "claude",
		"repository", "repositories", "commit", "pull request",
		"system.echo", "github.repository.list", ".list", ".get", ".search",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("the policy names %q; it must describe evidence, not vendors or tools", forbidden)
		}
	}
}

// It is instructions, so it is billed as instructions and reported as such.
// A policy that reached the model without appearing in the account would be
// the report's first silent cost.
func TestThePolicyIsReportedAsInstructions(t *testing.T) {
	history := []domain.Message{userTurn("oi")}
	built := BuildContext(ContextInput{
		Agent:   agentWith(""),
		Tools:   []domain.ToolDefinition{toolDef("system.echo")},
		History: history, Current: &history[0],
	})

	block, ok := built.Report.Block(domain.BlockInstructions)
	if !ok {
		t.Fatal("the policy was sent without being reported")
	}
	if block.Characters != GroundingPolicyCharacters() {
		t.Fatalf("instructions block = %d chars, want the policy's %d",
			block.Characters, GroundingPolicyCharacters())
	}
}

// The overhead is asserted, not described. A policy that grew into an essay
// would be one nobody reads and everybody pays for.
func TestThePolicyStaysSmall(t *testing.T) {
	// Raised once, from 200, and the raise is the record of a decision.
	//
	// The policy gained one paragraph — the one that says a read the user
	// implicitly asked for is taken rather than offered. It was added because
	// the live experiment showed both models complying with everything the
	// policy said and still answering "quer que eu investigue?", which is a
	// non-answer the user has to reply to before getting one.
	//
	// The cost is ~84 estimated tokens per turn, and only on turns that
	// declare capabilities. Measured against what it bought: the alternative
	// was a forced tool_choice, which would have spent a tool call on EVERY
	// turn including the ones already answered by evidence.
	//
	// ── Restated in calibrated units, 2026-08-30 ──────────────────
	//
	// This ceiling used to be 280, against a policy that measured ~253
	// tokens. Neither the policy nor its cost changed: the ESTIMATOR did.
	// It divided characters by four, and 159 real turns showed the true
	// density is roughly twice that (see the calibration note in
	// context.go). The same text now measures ~505.
	//
	// So the ceiling is restated at the same ~10% headroom over the same
	// unchanged text, rather than the test being deleted or the policy cut
	// to fit a number that was always wrong. The goalpost did not move; the
	// ruler was replaced.
	//
	// What it still catches is what it always caught: a policy that grows
	// into an essay nobody reads and everybody pays for, on every turn that
	// declares capabilities.
	// ── Raised to 700, 2026-09-07, and this is the decision record ──
	//
	// The policy gained a paragraph about systems this product does not
	// own: their present state must be read in the current turn, and an
	// earlier reading is stale rather than merely weak.
	//
	// It was added because a content agent with a real connection answered
	// "1.535 seguidores, 40.055 views" in a turn with ZERO tool calls. The
	// true figures were 163 and 224. Nothing in the previous policy was
	// violated in a way the model could notice — it had read that account
	// in an earlier turn and treated the numbers as known — so the gap was
	// the policy's, not the model's.
	//
	// The cost is ~133 estimated tokens per turn, and only on turns that
	// declare capabilities. Measured against what it buys: this is the
	// cheap half of the fix. The half that actually holds is the read
	// receipt, which does not depend on the model reading anything.
	//
	// The ceiling keeps ~10% headroom over the measured text, unchanged in
	// method from the two raises before it.
	const ceiling = 700
	if got := EstimateTokens(GroundingPolicyCharacters()); got > ceiling {
		t.Fatalf("the policy costs ~%d estimated tokens per turn, above the %d "+
			"this design budgeted; shorten it or justify the raise", got, ceiling)
	}
}

/* ── autonomous investigation, as a property of the policy ───────────── */

// The paragraph that closed the observed gap. It has to say two things, and
// a rewrite that drops either turns the platform back into one that offers
// to investigate instead of investigating.
func TestThePolicyTellsTheModelToActRatherThanOffer(t *testing.T) {
	lower := strings.ToLower(groundingPolicy)
	for _, must := range []string{
		// take the lookup
		"do it and then answer",
		// and specifically: offering is not a substitute
		"is not an answer",
	} {
		if !strings.Contains(lower, must) {
			t.Fatalf("the policy no longer says %q; both models were observed "+
				"asking permission instead of answering", must)
		}
	}
}

// And the half that keeps autonomy read-only. Without it, "act without
// asking" generalises to a capability that changes something — and the
// registry now contains one.
func TestThePolicyExemptsCapabilitiesThatChangeThings(t *testing.T) {
	if !strings.Contains(groundingPolicy, "CHANGES something is the exception") {
		t.Fatal("the policy no longer carves writes out of autonomous use")
	}
	if !strings.Contains(groundingPolicy, "only when the user asked for that change") {
		t.Fatal("the policy no longer says who authorises a change")
	}
}

// Proportionality survived the addition: a turn already answered by its
// context must still be answered from it, or Live Test C regresses.
func TestThePolicyStillPrefersTheContextItAlreadyHas(t *testing.T) {
	if !strings.Contains(groundingPolicy, "if the context already answers the question, answer from it") {
		t.Fatal("the policy lost the sentence that prevents a needless lookup")
	}
	if !strings.Contains(groundingPolicy, "not everything in reach") {
		t.Fatal("the policy lost the sentence that prevents a brute-force sweep")
	}
}

/* ── what the model is told a tool does ──────────────────────────────── */

// A read declares itself exactly as its author wrote it. Nothing is
// appended, so every tool that existed before this change is byte-identical
// on the wire.
func TestAReadToolDeclaresWhatItsAuthorWrote(t *testing.T) {
	d := domain.ToolDefinition{
		Name: "x.read", Title: "R", Description: "Reads a thing.", Effect: domain.EffectRead,
	}
	if got := d.DeclaredDescription(); got != "Reads a thing." {
		t.Fatalf("a read tool's declaration was rewritten: %q", got)
	}
}

// A write says so, in the one place the model actually reads. The effect
// was previously recorded and never sent, which made "never use a write
// without being asked" a rule the model had no way to apply.
func TestAWriteToolDeclaresThatItChangesThings(t *testing.T) {
	d := domain.ToolDefinition{
		Name: "x.write", Title: "W", Description: "Moves a thing.", Effect: domain.EffectWrite,
	}
	got := d.DeclaredDescription()
	if !strings.HasPrefix(got, "Moves a thing.") {
		t.Fatalf("the author's own description was lost: %q", got)
	}
	if !strings.Contains(got, "CHANGES data") {
		t.Fatalf("a write tool does not declare its effect: %q", got)
	}
	if !strings.Contains(got, "unless the user asked") {
		t.Fatalf("a write tool does not declare who authorises it: %q", got)
	}
}

// The notice is generated from the effect, so it names no tool, no module
// and no vendor, and a capability registered next year is covered on the
// day it is registered.
func TestTheWriteNoticeNamesNothingSpecific(t *testing.T) {
	lower := strings.ToLower(writeNoticeFor(domain.EffectWrite))
	for _, name := range []string{"github", "job", "radar", "corsi", "litellm", "opportunity"} {
		if strings.Contains(lower, name) {
			t.Fatalf("the write notice names %q; it is a platform rule", name)
		}
	}
}

// The report prices what is actually sent. A declaration that grew on the
// wire and not in the estimate would understate exactly the turns that
// carry a write tool.
func TestTheReportPricesTheDeclarationThatIsSent(t *testing.T) {
	write := domain.ToolDefinition{
		Name: "x.write", Title: "W", Description: "Moves a thing.", Effect: domain.EffectWrite,
	}
	read := domain.ToolDefinition{
		Name: "x.write", Title: "W", Description: "Moves a thing.", Effect: domain.EffectRead,
	}
	if toolDeclarationChars(write) <= toolDeclarationChars(read) {
		t.Fatal("the write notice is sent but not counted")
	}
}

// writeNoticeFor exists so the test can read the notice without exporting
// it from the domain: the same definition, differenced.
func writeNoticeFor(e domain.ToolEffect) string {
	d := domain.ToolDefinition{Description: "", Effect: e}
	return d.DeclaredDescription()
}
