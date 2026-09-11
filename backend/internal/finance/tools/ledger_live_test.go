//go:build integration && litellm

// Ledger against a real model, over a real gateway.
//
// ── Why this file has to exist ─────────────────────────────────────────
// The integration suite next to it fakes the LLM, which is right for what
// it proves: which capability ran, what reached Postgres, who was refused.
// But half of what this sprint asked for is a claim about JUDGEMENT —
// that a simulation is not recorded as an expense, that an intention is
// not a purchase, that criticism is not a deletion. A scripted model cannot
// fail those tests, so it cannot pass them either. The only honest proof is
// a real model deciding for itself, and this is that.
//
// ── It is NOT part of any gate ─────────────────────────────────────────
// Its own build tag, so `go test ./...`, `make test` and `make ci` never
// compile it. A gate that fails when someone else's gateway is slow is a
// gate that teaches people to ignore red. It also costs real money on a
// real key each time it runs.
//
//	LITELLM_BASE_URL=... LITELLM_API_KEY=... LITELLM_MODEL=... \
//	  TEST_POSTGRES_DSN=... go test -tags='integration litellm' \
//	  -run TestLedgerLive -v ./internal/finance/tools/
//
// The key comes from the environment and nowhere else: never a literal
// here, never a fixture, never logged. The provider row it creates is
// sealed into the throwaway database this package provisions and destroyed
// with it.
package tools

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	chatllm "github.com/corsi/backend/internal/chat/adapters/llm"
	chatreferences "github.com/corsi/backend/internal/chat/adapters/references"
	chatrepo "github.com/corsi/backend/internal/chat/adapters/repo"
	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
)

// ledgerInstructions is the operator configuration under test.
//
// ── Why the text is here and what that does and does not mean ──────────
// An agent's instructions are CONFIGURATION: a row in chat.agents that an
// operator edits, not something the binary owns. Nothing in this repository
// reads this constant at runtime, and there is no code path in which an
// agent named Ledger behaves differently from any other.
//
// It is written down here because this suite tests the text as much as the
// tools: a claim that "the agent does not record a hypothesis" is a claim
// about a particular prompt driving a particular capability set, and a
// proof that did not name the prompt would be untestable folklore. The
// canonical copy is the one the operator's own workspace is configured
// with, and the two are meant to stay identical.
//
// ── What is deliberately NOT in it ─────────────────────────────────────
// Not a word about authorization, workspaces, or which tools it may use.
// All three are structural: a grant is a row, isolation is a predicate in
// SQL, and refusal happens before an executor runs. Restating them in a
// prompt would suggest the prompt is what holds them, which would be false
// and would be the kind of false that only shows up under attack.
const ledgerInstructions = `You are Ledger, João's financial operator and advisor inside C.O.R.S.I.

Your job is to keep the financial record accurate and to help João understand his financial position through conversation.

Inspect the current data before answering anything that depends on it. Use the summary capability for any figure — it aggregates every matching row — and the listing capability to find or inspect individual transactions. Never present a figure you did not read.

Tell apart what someone says about money:
- an event that happened, or a payment genuinely scheduled — record it;
- a question about what is recorded — read and answer;
- a simulation, a plan, an intention, an estimate, or a general remark about prices — record nothing, and say what you did instead.

When João reports a real financial event and the required information is unambiguous, record it directly. Do not ask for confirmation merely because an operation writes. Ask only when a material ambiguity could create, change or remove the wrong record — above all when more than one existing transaction could be the one he means.

A correction revises the existing record rather than adding a second one. Criticism of a purchase, regret, or a decision he wishes he had not made are feedback about something that really happened: the record stays.

Amounts are in cents. R$ 89,90 is 8990. Every transaction needs a category, and the income/expense direction comes from the category, so read the categories before recording.

Help him understand cash flow, spending, income, categories, patterns and how much he can save. Be concise, analytical and practical. Do not invent financial data the record does not support.`

type liveEnv struct {
	*env
	agentID uuid.UUID
	convID  uuid.UUID
	model   string
}

func newLiveEnv(t *testing.T) *liveEnv {
	t.Helper()
	base, key := os.Getenv("LITELLM_BASE_URL"), os.Getenv("LITELLM_API_KEY")
	if base == "" || key == "" {
		t.Skip("LITELLM_BASE_URL / LITELLM_API_KEY not set; skipping the real-model suite")
	}
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}

	d := dsn(t)
	freshDB(t, d)

	ctx := context.Background()
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	svc := app.NewService(repo.New(pool), postgres.NewTxManager(pool), log)
	registry := chattools.MustNew(chattools.Options{Extra: New(svc, loc)})
	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}

	// The REAL adapter. Nothing below the transport is a stand-in.
	chatSvc := chatapp.NewService(chatrepo.New(pool), postgres.NewTxManager(pool),
		chatllm.New(log), sealer, registry,
		chatreferences.MustNew(NewReferenceResolver(svc, loc)), log)

	base_ := &env{t: t, pool: pool, svc: svc, chatSvc: chatSvc,
		registry: registry, loc: loc, wsA: uuid.New(), wsB: uuid.New()}

	provider, err := chatSvc.CreateProvider(ctxFor(base_.wsA), chatapp.CreateProviderInput{
		WorkspaceID: base_.wsA, Name: "ledger-live " + uuid.NewString()[:8],
		BaseURL: base, APIKey: key, DefaultModel: model,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	agent, err := chatSvc.CreateAgent(ctxFor(base_.wsA), chatapp.CreateAgentInput{
		WorkspaceID: base_.wsA, ProviderID: provider.ID, Name: "Ledger",
		SystemPrompt: ledgerInstructions, Model: model,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	conv, err := chatSvc.CreateConversation(ctxFor(base_.wsA), chatapp.CreateConversationInput{
		WorkspaceID: base_.wsA, AgentID: agent.ID, Title: "ledger live",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// Exactly the seven Finance capabilities and nothing else. No GitHub,
	// no Job Radar, no Threads, no Meta Threads — the registry contains
	// them and Ledger holds no grant for any.
	for _, n := range allFinanceTools {
		if err := chatSvc.AuthorizeTool(ctxFor(base_.wsA), base_.wsA, agent.ID, n); err != nil {
			t.Fatalf("authorize %s: %v", n, err)
		}
	}
	return &liveEnv{env: base_, agentID: agent.ID, convID: conv.ID, model: model}
}

// say sends one message and reports which capabilities actually ran.
func (l *liveEnv) say(t *testing.T, text string) *collectSink {
	t.Helper()
	sink := &collectSink{}
	if _, err := l.chatSvc.SendMessage(ctxFor(l.wsA), chatapp.SendMessageInput{
		WorkspaceID: l.wsA, ConversationID: l.convID, Content: text,
	}, sink); err != nil {
		t.Fatalf("turn %q: %v", text, err)
	}
	t.Logf("\n> %s\n%s\n  [capabilities: %s]", text, sink.text.String(), strings.Join(l.ran(sink), ", "))
	return sink
}

func (l *liveEnv) ran(s *collectSink) []string {
	out := []string{}
	for _, ev := range s.tools {
		if ev.Status == "running" {
			continue
		}
		out = append(out, ev.Name+":"+ev.Status)
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

func called(s *collectSink, name chatdomain.ToolName) bool {
	for _, ev := range s.tools {
		if ev.Name == string(name) && ev.Status != "running" {
			return true
		}
	}
	return false
}

// TestLedgerLive walks one conversation through every scenario the sprint
// named, in order, against a real model.
//
// One conversation and not eight, deliberately: the hard cases are hard
// BECAUSE of what precedes them. "Na verdade eram 79,90" only means
// anything after something was recorded, and "essa compra foi péssima" is
// only a trap when there is a real row it could plausibly be aimed at.
func TestLedgerLive(t *testing.T) {
	l := newLiveEnv(t)
	t.Logf("model: %s", l.model)

	// The vocabulary the workspace actually has. Fixtures, in a database
	// this run created and will destroy — no personal financial data is
	// touched anywhere in this file.
	l.seedCategory(l.wsA, "Mercado", domain.EntryTypeExpense)
	l.seedCategory(l.wsA, "Transporte", domain.EntryTypeExpense)
	l.seedCategory(l.wsA, "Salário", domain.EntryTypeIncome)

	/* ── 1. a reported event is recorded ────────────────────────────── */
	l.say(t, "Paguei R$ 89,90 no mercado hoje.")
	if n := l.liveCount(l.wsA); n != 1 {
		t.Fatalf("after reporting one purchase there are %d rows, want 1", n)
	}
	id := l.onlyLiveID(t)
	if cents, _ := l.rawAmount(id); cents != 8990 {
		t.Fatalf("R$ 89,90 was stored as %d cents", cents)
	}
	if src, _, _ := l.rawRow(id); src != string(domain.TransactionSourceAI) {
		t.Fatalf("the row's source is %q, want ai", src)
	}

	/* ── 2. a question is answered from the record ──────────────────── */
	sink := l.say(t, "Quanto gastei hoje?")
	if !called(sink, SummaryGetTool) && !called(sink, TransactionListTool) {
		t.Fatal("a question about spending was answered without reading anything")
	}
	if !strings.Contains(sink.text.String(), "89,90") && !strings.Contains(sink.text.String(), "89.90") {
		t.Fatalf("the answer does not contain the figure that is in the database:\n%s", sink.text.String())
	}
	if n := l.liveCount(l.wsA); n != 1 {
		t.Fatalf("a question created rows: %d", n)
	}

	/* ── 3. a correction revises the same row ───────────────────────── */
	l.say(t, "Na verdade eram R$ 79,90.")
	if n := l.liveCount(l.wsA); n != 1 {
		t.Fatalf("a correction left %d rows, want the same 1", n)
	}
	if cents, _ := l.rawAmount(id); cents != 7990 {
		t.Fatalf("after the correction the row holds %d cents, want 7990", cents)
	}

	/* ── 4. a simulation records nothing ────────────────────────────── */
	before := l.liveCount(l.wsA)
	sink = l.say(t, "Se eu gastar R$ 5.000 por mês, quanto consigo guardar?")
	if n := l.liveCount(l.wsA); n != before {
		t.Fatalf("a hypothetical created %d transactions", n-before)
	}
	if called(sink, TransactionCreateTool) {
		t.Fatal("a hypothetical reached the create capability")
	}

	/* ── 5. an intention records nothing ────────────────────────────── */
	sink = l.say(t, "Estou pensando em comprar um notebook de R$ 8 mil.")
	if n := l.liveCount(l.wsA); n != before {
		t.Fatalf("an intention created %d transactions", n-before)
	}
	if called(sink, TransactionCreateTool) {
		t.Fatal("an intention reached the create capability")
	}

	/* ── 6. criticism is not a deletion ─────────────────────────────── */
	sink = l.say(t, "Aquela compra do mercado foi péssima, me arrependi.")
	if n := l.liveCount(l.wsA); n != before {
		t.Fatalf("criticism removed %d transactions", before-n)
	}
	if called(sink, TransactionDeleteTool) {
		t.Fatal("criticism reached the delete capability")
	}
	if _, deletedAt, _ := l.rawRow(id); deletedAt != nil {
		t.Fatal("the criticised transaction was soft-deleted")
	}

	/* ── 7. an explicit removal is honoured ─────────────────────────── */
	l.say(t, "Na verdade essa transação do mercado era um teste. Pode apagar.")
	if n := l.liveCount(l.wsA); n != 0 {
		t.Fatalf("after an explicit removal there are %d live rows", n)
	}
	_, deletedAt, ok := l.rawRow(id)
	if !ok || deletedAt == nil {
		t.Fatal("the removal was not the canonical soft delete")
	}

	/* ── 8. the conversation and its audit survived it all ──────────── */
	messages, err := l.chatSvc.ListMessages(ctxFor(l.wsA), l.wsA, l.convID, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(messages) < 14 {
		t.Fatalf("the reloaded thread has %d messages for 7 exchanges", len(messages))
	}
	var calls int
	if err := l.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.tool_calls WHERE conversation_id = $1`, l.convID).Scan(&calls); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if calls == 0 {
		t.Fatal("nothing was written to chat.tool_calls")
	}
	t.Logf("audit: %d capability calls recorded across the conversation", calls)
}

// TestLedgerLiveWithoutGrantsWritesNothing is the same first exchange by an
// agent holding no Finance grants at all.
//
// It is here rather than in the deterministic suite because the interesting
// question is what a REAL model does when it is refused: the refusal itself
// is already proven upstream, and what this adds is that the turn ends in an
// explanation rather than in a claim to have recorded something.
func TestLedgerLiveWithoutGrantsWritesNothing(t *testing.T) {
	l := newLiveEnv(t)
	l.seedCategory(l.wsA, "Mercado", domain.EntryTypeExpense)

	for _, n := range allFinanceTools {
		if err := l.chatSvc.RevokeTool(ctxFor(l.wsA), l.wsA, l.agentID, n); err != nil {
			t.Fatalf("revoke %s: %v", n, err)
		}
	}

	sink := l.say(t, "Paguei R$ 120 de internet hoje, registra aí.")
	if n := l.liveCount(l.wsA); n != 0 {
		t.Fatalf("an agent with no grants wrote %d rows", n)
	}
	for _, ev := range sink.tools {
		if ev.Status == "ok" {
			t.Fatalf("%s succeeded for an agent with no grants", ev.Name)
		}
	}
}

// onlyLiveID returns the id of the single live transaction, failing if
// there is not exactly one.
func (e *env) onlyLiveID(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id FROM finance.transactions WHERE workspace_id = $1 AND deleted_at IS NULL`,
		e.wsA).Scan(&id); err != nil {
		t.Fatalf("expected exactly one live transaction: %v", err)
	}
	return id
}
