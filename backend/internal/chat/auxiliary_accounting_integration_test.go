//go:build integration

// Integration tests for auxiliary accounting (v1.1.0).
//
// ── What is being fixed here, before anything uses it ──────────────────
// The next batch adds an operation that spends tokens without producing a
// turn: memory consolidation reads a conversation and asks the model what
// is worth remembering. Consumption in this module is derived from
// chat.messages, so such an operation has to write a row there or its spend
// would be invisible to every total and to the daily gate.
//
// The row it writes is a receipt: role assistant, kind auxiliary, no words.
// That splits this table's readers in two, and the split is the whole
// subject of this file:
//
//	conversation semantics  →  turns only     transcript, history, truncate
//	accounting semantics    →  both kinds     usage, budget
//
// No provider call is made anywhere below. The operation does not exist
// yet; what exists is the record it will write, and these tests hold the
// two halves of the contract apart so the batch that adds the call cannot
// quietly break either.
//
// The harness lives in chat_integration_test.go.
package chat

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/adapters/repo"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

// receipt writes what a billable non-turn operation records.
//
// It goes through the production repository and the production domain
// validation rather than through raw SQL, because the point of the test is
// that this row is writable exactly as designed and readable exactly as
// intended — a hand-written INSERT would prove something about the test's
// SQL instead.
func (e *env) receipt(ws uuid.UUID, conversationID string, prompt, completion int, cost *float64) *domain.Message {
	e.t.Helper()
	m := &domain.Message{
		ID:               uuid.New(),
		WorkspaceID:      ws,
		ConversationID:   uuid.MustParse(conversationID),
		Role:             domain.RoleAssistant,
		Kind:             domain.KindAuxiliary,
		Model:            "test-model",
		PromptTokens:     prompt,
		CompletionTokens: completion,
		UsageSource:      domain.UsageProvider,
		Cost:             cost,
	}
	if cost != nil {
		rate := 0.000001
		m.InputCostPerToken, m.OutputCostPerToken = &rate, &rate
	}
	if err := m.Validate(); err != nil {
		e.t.Fatalf("a receipt is not a valid message: %v", err)
	}
	if err := repo.New(e.pool).Messages.Create(context.Background(), m); err != nil {
		e.t.Fatalf("write receipt: %v", err)
	}
	return m
}

/* ── the record itself ───────────────────────────────────────────────── */

// TestAReceiptCarriesNoWords is the invariant that bounds every other
// mistake in this file. A reader that forgets to filter by kind shows an
// empty bubble; it cannot leak auxiliary text into a conversation, because
// there is no text to leak.
func TestAReceiptCarriesNoWords(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	speaking := &domain.Message{
		ID: uuid.New(), WorkspaceID: e.wsA, ConversationID: uuid.MustParse(s.conversationID),
		Role: domain.RoleAssistant, Kind: domain.KindAuxiliary, Content: "eu não deveria falar",
	}
	if err := speaking.Validate(); err == nil {
		t.Fatal("the domain accepted an auxiliary message with content")
	}
	// And the database refuses it too, so the guarantee does not depend on
	// every future writer remembering to call Validate.
	if err := repo.New(e.pool).Messages.Create(context.Background(), speaking); err == nil {
		t.Fatal("the database accepted an auxiliary message with content")
	}

	asUser := &domain.Message{
		ID: uuid.New(), WorkspaceID: e.wsA, ConversationID: uuid.MustParse(s.conversationID),
		Role: domain.RoleUser, Kind: domain.KindAuxiliary,
	}
	if err := asUser.Validate(); err == nil {
		t.Fatal("the domain accepted a receipt that was not assistant-side")
	}
}

// TestExistingRowsAreTurns is what the migration promises about everything
// that was already there: nothing has ever been written to this table but
// turns, and the default says so without a backfill.
func TestExistingRowsAreTurns(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{{Delta: "resposta"}}
	e.send(e.wsA, s.conversationID, "pergunta")

	var kinds []string
	rows, err := e.pool.Query(context.Background(),
		`SELECT kind FROM chat.messages WHERE conversation_id = $1`, s.conversationID)
	if err != nil {
		t.Fatalf("read kinds: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan kind: %v", err)
		}
		kinds = append(kinds, k)
	}
	if len(kinds) != 2 {
		t.Fatalf("a turn wrote %d rows, want the question and the answer", len(kinds))
	}
	for _, k := range kinds {
		if k != "turn" {
			t.Fatalf("an ordinary turn wrote kind %q", k)
		}
	}
}

/* ── accounting sees it ──────────────────────────────────────────────── */

// TestAReceiptIsCountedAsConsumption is the reason the row exists at all.
func TestAReceiptIsCountedAsConsumption(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	before := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if before.TotalTokens != 0 {
		t.Fatalf("a fresh agent already consumed %d tokens", before.TotalTokens)
	}

	e.receipt(e.wsA, s.conversationID, 800, 120, f64p(0.00092))

	for _, path := range []string{
		"/chat/agents/" + s.agentID + "/usage",
		"/chat/conversations/" + s.conversationID + "/usage",
		"/chat/usage",
	} {
		got := e.usage(e.wsA, path)
		if got.TotalPromptTokens != 800 || got.TotalCompletionTokens != 120 {
			t.Fatalf("%s: tokens = %d/%d, want 800/120", path, got.TotalPromptTokens, got.TotalCompletionTokens)
		}
		if got.Messages != 1 {
			t.Fatalf("%s: counted %d billable operations, want 1", path, got.Messages)
		}
		if got.EstimatedCost != 0.00092 {
			t.Fatalf("%s: cost = %v, want 0.00092", path, got.EstimatedCost)
		}
	}
}

// TestAnUnpricedReceiptIsCountedAsUnknown holds the honesty rule of the
// usage report over the new row: spend nobody could price is reported as
// missing, never as zero.
func TestAnUnpricedReceiptIsCountedAsUnknown(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.receipt(e.wsA, s.conversationID, 500, 50, nil)

	got := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if got.UnpricedMessages != 1 {
		t.Fatalf("unpriced = %d, want 1", got.UnpricedMessages)
	}
	if got.EstimatedCost != 0 {
		t.Fatalf("cost = %v, want 0 with the gap reported beside it", got.EstimatedCost)
	}
	if got.TotalTokens != 550 {
		t.Fatalf("tokens = %d, want 550 even though the cost is unknown", got.TotalTokens)
	}
	if got.Priced {
		t.Fatal("the report claims to be priced while a billable operation is not")
	}
}

// TestAReceiptCountsAgainstTheDailyBudget is the one that makes the whole
// design worth its cost. If this fails, the next batch's consolidation is a
// route that spends money outside every limit the v1.0.0 built.
func TestAReceiptCountsAgainstTheDailyBudget(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.setBudget(e.wsA, s.agentID, intp(1000), nil)

	// Nothing consumed yet: the turn would run.
	if status := e.budget(e.wsA, s.agentID); status.Status.Blocked {
		t.Fatal("a fresh agent with a limit is already blocked")
	}

	e.receipt(e.wsA, s.conversationID, 900, 200, nil)

	status := e.budget(e.wsA, s.agentID)
	if status.Usage.Tokens != 1100 {
		t.Fatalf("the day counts %d tokens, want the 1100 the receipt cost", status.Usage.Tokens)
	}
	if !status.Status.Blocked {
		t.Fatal("an auxiliary operation spent past the daily limit and the gate did not notice")
	}
	// And the gate acts on it: the next turn is refused before the provider.
	e.wantBlocked(e.wsA, s.conversationID, "token_limit_reached")
}

/* ── the conversation does not ───────────────────────────────────────── */

func TestAReceiptIsNotInTheTranscript(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{{Delta: "resposta"}}
	e.send(e.wsA, s.conversationID, "pergunta")

	e.receipt(e.wsA, s.conversationID, 100, 10, nil)

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("the transcript has %d messages, want the question and the answer", len(msgs))
	}
	for _, m := range msgs {
		if m.Content == "" {
			t.Fatalf("an empty row reached the transcript: %+v", m)
		}
	}
}

// TestAReceiptIsNotReplayedToTheModel reads the request the provider
// actually received, which is the only place "did it enter the context"
// can be answered without trusting a layer above it.
func TestAReceiptIsNotReplayedToTheModel(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{{Delta: "primeira"}}
	e.send(e.wsA, s.conversationID, "primeira pergunta")

	e.receipt(e.wsA, s.conversationID, 4000, 400, nil)

	e.llm.script = []ports.StreamEvent{{Delta: "segunda"}}
	e.send(e.wsA, s.conversationID, "segunda pergunta")

	for _, m := range e.llm.lastRequest.Messages {
		if m.Content == "" {
			t.Fatalf("an empty message was sent to the provider: %+v", e.llm.lastRequest.Messages)
		}
	}

	// The context report is the second witness, and the sharper one: an
	// auxiliary row that reached the builder would be counted as a history
	// item and then excluded as an empty turn. Neither happened.
	report := e.lastReport(e.wsA, s.conversationID)
	if report == nil {
		t.Fatal("the turn recorded no context report")
	}
	for _, b := range report.Blocks {
		for _, ex := range b.Exclusions {
			if ex.Reason == "empty_turn" {
				t.Fatalf("the builder saw a receipt and excluded it as an empty turn: %+v", b)
			}
		}
		// The thread behind the question is the first exchange, two turns,
		// and the receipt written between them is not one of them. The
		// question being answered is counted separately, in its own block.
		if b.Kind == "history" && b.Items != 2 {
			t.Fatalf("history carried %d items, want the 2 real turns behind the question", b.Items)
		}
		if b.Kind == "current_message" && b.Items != 1 {
			t.Fatalf("the current message block carried %d items, want 1", b.Items)
		}
	}
}

// TestAReceiptIsNotATurnToTruncateFrom covers the lookup that backs
// regenerate and edit. Offering to regenerate an accounting record is not a
// thing an interface should be able to do.
func TestAReceiptIsNotATurnToTruncateFrom(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{{Delta: "resposta"}}
	e.send(e.wsA, s.conversationID, "pergunta")

	r := e.receipt(e.wsA, s.conversationID, 100, 10, nil)

	rec := e.do("DELETE", "/chat/conversations/"+s.conversationID+"/messages/"+itoa64(r.Seq), e.wsA, nil)
	wantStatus(t, rec, http.StatusNotFound)
}

// TestTruncatingAThreadKeepsItsReceipts is the decision this batch had to
// take deliberately rather than inherit.
//
// Regenerating an answer replaces turns. It does not un-spend what an
// unrelated auxiliary operation cost earlier in the same thread, and
// deleting those rows would quietly reduce the day's accounted spend — the
// one direction accounting must never move on its own.
func TestTruncatingAThreadKeepsItsReceipts(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{{Delta: "resposta"}}
	e.send(e.wsA, s.conversationID, "primeira")

	e.llm.script = []ports.StreamEvent{{Delta: "outra resposta"}}
	e.send(e.wsA, s.conversationID, "segunda")

	// The receipt is written AFTER the turn that is about to be cut, so it
	// sits inside the range the delete covers. A receipt written before it
	// would survive by arithmetic rather than by the rule, and the test
	// would pass without exercising anything.
	e.receipt(e.wsA, s.conversationID, 700, 70, nil)

	// Cut from the second question: two turns go.
	second := e.messages(e.wsA, s.conversationID)[2]
	rec := e.do("DELETE", "/chat/conversations/"+s.conversationID+"/messages/"+itoa64(second.Seq), e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)

	if got := len(e.messages(e.wsA, s.conversationID)); got != 2 {
		t.Fatalf("the thread kept %d messages, want the first exchange", got)
	}
	usage := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if usage.TotalPromptTokens < 700 {
		t.Fatalf("truncation erased the receipt: prompt tokens are down to %d", usage.TotalPromptTokens)
	}
}

func TestAReceiptIsWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.receipt(e.wsA, s.conversationID, 300, 30, nil)

	if got := e.usage(e.wsB, "/chat/usage"); got.TotalTokens != 0 {
		t.Fatalf("wsB sees %d tokens of wsA's spend", got.TotalTokens)
	}
}
