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
	svc := app.NewService(repo.New(pool), postgres.NewTxManager(pool), log, loc)
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

/* ── daily capture, the way it is actually typed ─────────────────────── */

// capturedRow is one stored transaction, read straight from the columns.
type capturedRow struct {
	ID          uuid.UUID
	AmountCents int64
	OccurredAt  time.Time
	Source      string
	Type        string
}

// liveRows reads every live transaction in a workspace, from the database.
//
// Through SQL rather than through the service that wrote them, for the
// reason the deterministic suite gives: a readback through the same code
// path agrees with a consistent mistake, and the whole claim here is about
// what is IN the ledger.
func (l *liveEnv) liveRows(t *testing.T) []capturedRow {
	t.Helper()
	rows, err := l.pool.Query(context.Background(),
		`SELECT id, amount_cents, occurred_at, source::text, type::text
		   FROM finance.transactions
		  WHERE workspace_id = $1 AND deleted_at IS NULL
		  ORDER BY created_at`, l.wsA)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer rows.Close()
	var out []capturedRow
	for rows.Next() {
		var r capturedRow
		if err := rows.Scan(&r.ID, &r.AmountCents, &r.OccurredAt, &r.Source, &r.Type); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read back: %v", err)
	}
	return out
}

// receiptFor is the turn's own answer to "did anything change", derived by
// the runtime from execution records.
//
// Asserted on deliberately: the point of daily capture is that the operator
// is told the expense was recorded ONLY when it was, and "the model said
// so" is not evidence. This reads the same receipt the Telegram footer and
// the web client read.
func (l *liveEnv) receiptFor(t *testing.T, messageID uuid.UUID) chatdomain.WriteReceipt {
	t.Helper()
	receipts, err := l.chatSvc.WriteReceipts(ctxFor(l.wsA), l.wsA, []uuid.UUID{messageID})
	if err != nil {
		t.Fatalf("write receipts: %v", err)
	}
	return receipts[messageID]
}

// sendAndCapture runs one turn and hands back BOTH the persisted assistant
// turn and what was observed while it ran.
//
// Both, because the two answer different questions and a test usually needs
// each: the message id resolves the turn's write receipt, which is the
// evidence that something was executed, and the sink says which
// capabilities ran, which is how a test can tell "read then wrote" from
// "wrote blind".
func (l *liveEnv) sendAndCapture(t *testing.T, text string) (uuid.UUID, *collectSink) {
	t.Helper()
	sink := &collectSink{}
	msg, err := l.chatSvc.SendMessage(ctxFor(l.wsA), chatapp.SendMessageInput{
		WorkspaceID: l.wsA, ConversationID: l.convID, Content: text,
	}, sink)
	if err != nil {
		t.Fatalf("turn %q: %v", text, err)
	}
	t.Logf("\n> %s\n%s\n  [capabilities: %s]", text, sink.text.String(), strings.Join(l.ran(sink), ", "))
	return msg.ID, sink
}

// TestLedgerLiveDailyCapture is the sprint's daily-expense claim, phrase by
// phrase, against a real model.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE OPERATOR TYPES ONE LINE; THE LEDGER HOLDS THE RIGHT ROW
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why each phrase gets its own environment ───────────────────────────
// Because these are four independent product claims and a shared
// conversation would let one of them pass on the strength of another's
// context. "Uber 28 reais agora" has to work as the FIRST thing somebody
// says, because on a phone it usually is. A subtest per phrase also means a
// failure names exactly which sentence the model got wrong.
//
// ── What each one asserts, and why the prose is not one of them ────────
// Exactly one live row; the exact integer of cents; `source = ai`, so the
// origin badge cannot lie about where the row came from; the calendar day,
// in the reporting zone; and a write receipt reporting one executed write.
// Nothing here reads the answer the model produced. The model's sentence is
// logged for a human to look at and is never the evidence.
//
// ── The zone ───────────────────────────────────────────────────────────
// Every date comparison is made in the suite's own reporting location,
// which is the location the tools were built with. Comparing in UTC would
// pass in the morning and fail after 21:00 in São Paulo, which is precisely
// the class of bug the midday stamping rule exists to prevent.
func TestLedgerLiveDailyCapture(t *testing.T) {
	cases := []struct {
		name string
		// text is EXACTLY what the operator types. Not paraphrased.
		text string
		// wantCents is the integer that must be in the column. The whole
		// sprint fails if "28 reais" lands as 28.
		wantCents int64
		// dayOffset is which calendar day, counted from today in the
		// reporting zone. Zero is today, -1 is yesterday.
		dayOffset int
		// category is the vocabulary the workspace is given, so the model
		// has something correct to choose and the test is not really
		// measuring whether a category happened to fit.
		category string
	}{
		{
			name:      "mercado sem verbo",
			text:      "Mercado 186,43",
			wantCents: 18643,
			dayOffset: 0,
			category:  "Mercado",
		},
		{
			name: "uber em reais inteiros, agora",
			// The trap: "28 reais" has no centavos, and a model that sends
			// the number it read records twenty-eight centavos.
			text:      "Uber 28 reais agora",
			wantCents: 2800,
			dayOffset: 0,
			category:  "Transporte",
		},
		{
			name:      "almoco com centavos",
			text:      "Gastei 37,90 no almoço",
			wantCents: 3790,
			dayOffset: 0,
			category:  "Alimentação",
		},
		{
			name: "ontem, data relativa",
			// The trap this sprint's Phase A closes: the model has no clock,
			// so "ontem" is only answerable by counting from the `today`
			// that a Finance read reports.
			text:      "Ontem gastei 90 no cinema",
			wantCents: 9000,
			dayOffset: -1,
			category:  "Lazer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLiveEnv(t)
			t.Logf("model: %s", l.model)
			l.seedCategory(l.wsA, tc.category, domain.EntryTypeExpense)

			messageID, _ := l.sendAndCapture(t, tc.text)

			rows := l.liveRows(t)
			if len(rows) != 1 {
				t.Fatalf("%q produced %d live transactions, want exactly 1", tc.text, len(rows))
			}
			got := rows[0]

			if got.AmountCents != tc.wantCents {
				t.Errorf("%q stored %d cents, want %d", tc.text, got.AmountCents, tc.wantCents)
			}
			if got.Source != string(domain.TransactionSourceAI) {
				t.Errorf("%q stored source %q, want ai", tc.text, got.Source)
			}
			if got.Type != string(domain.EntryTypeExpense) {
				t.Errorf("%q stored type %q, want expense", tc.text, got.Type)
			}

			// The day, resolved in the reporting zone from the same clock
			// the tools read, not from this process's wall clock.
			now, err := l.svc.Now(context.Background())
			if err != nil {
				t.Fatalf("clock: %v", err)
			}
			wantDay := now.In(l.loc).AddDate(0, 0, tc.dayOffset).Format("2006-01-02")
			gotDay := got.OccurredAt.In(l.loc).Format("2006-01-02")
			if gotDay != wantDay {
				t.Errorf("%q landed on %s, want %s", tc.text, gotDay, wantDay)
			}

			// The receipt, derived from execution records. One executed
			// write, nothing failed, nothing refused.
			receipt := l.receiptFor(t, messageID)
			if !receipt.Confirmed() {
				t.Errorf("%q: the turn's receipt does not confirm any write (executed=%d failed=%d refused=%d)",
					tc.text, receipt.Executed, receipt.Failed, receipt.Refused)
			}
			if receipt.Executed != 1 {
				t.Errorf("%q: receipt reports %d executed writes, want 1", tc.text, receipt.Executed)
			}
			if receipt.Failed != 0 || receipt.Refused != 0 {
				t.Errorf("%q: receipt reports failed=%d refused=%d, want 0 and 0",
					tc.text, receipt.Failed, receipt.Refused)
			}
			t.Logf("%-28s %7d cents · %s · source=%s · receipt executed=%d",
				tc.text, got.AmountCents, gotDay, got.Source, receipt.Executed)
		})
	}
}

/* ── monthly commitment, against a real model ────────────────────────── */

// liveMonth reads a month through the application service, so an assertion
// is about the persisted state and never about the model's prose.
func (l *liveEnv) liveMonth(t *testing.T) app.MonthlyCommitmentView {
	t.Helper()
	v, err := l.svc.GetMonthlyCommitment(ctxFor(l.wsA), app.GetMonthlyCommitmentInput{
		WorkspaceID: l.wsA,
	})
	if err != nil {
		t.Fatalf("read month: %v", err)
	}
	return v
}

func (l *liveEnv) occurrenceOf(t *testing.T, entryID uuid.UUID) domain.RecurringOccurrence {
	t.Helper()
	for _, line := range l.liveMonth(t).Lines {
		if line.RecurringEntryID == entryID {
			return line.Occurrence
		}
	}
	t.Fatalf("the month has no occurrence for %s", entryID)
	return domain.RecurringOccurrence{}
}

// seedLiveRecurring creates a synthetic obligation that has been running
// for a year, so the current month is one it applies to.
func (l *liveEnv) seedLiveRecurring(
	t *testing.T, cat *domain.Category, desc string, cents int64, dueDay int, varies bool,
) *domain.RecurringEntry {
	t.Helper()
	f, err := l.svc.CreateRecurringEntry(ctxFor(l.wsA), app.CreateRecurringEntryInput{
		WorkspaceID: l.wsA, Description: desc, AmountCents: cents,
		CategoryID: cat.ID, DueDay: dueDay, AmountVaries: varies,
	})
	if err != nil {
		t.Fatalf("seed recurring %s: %v", desc, err)
	}
	if _, err := l.pool.Exec(context.Background(),
		`UPDATE finance.recurring_entries SET starts_at = now() - interval '1 year' WHERE id = $1`,
		f.ID); err != nil {
		t.Fatalf("backdate %s: %v", desc, err)
	}
	reloaded, err := l.svc.GetRecurringEntry(ctxFor(l.wsA), l.wsA, f.ID)
	if err != nil {
		t.Fatalf("reload %s: %v", desc, err)
	}
	return reloaded
}

// grantMonthlyCommitment adds the four monthly-commitment capabilities to
// the live agent, on top of the transaction set newLiveEnv already granted.
func (l *liveEnv) grantMonthlyCommitment(t *testing.T) {
	t.Helper()
	for _, n := range allOccurrenceTools {
		if err := l.chatSvc.AuthorizeTool(ctxFor(l.wsA), l.wsA, l.agentID, n); err != nil {
			t.Fatalf("authorize %s: %v", n, err)
		}
	}
}

// TestLedgerLiveMonthlyCommitment is the conversational half of this
// sprint, against a real model.
//
// ══════════════════════════════════════════════════════════════════════
//
//	READ THE MONTH, THEN WRITE ONE ROW, AND ASK WHEN IT IS AMBIGUOUS
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why these cannot be proved with a scripted model ───────────────────
// Every claim here is about JUDGEMENT. That a question is answered from a
// read rather than from the recurrences; that "paguei a internet" settles
// exactly one bill; that "marquei errado" reverses it rather than marking
// something else; and, above all, that two obligations both plausibly
// called "internet" produce a QUESTION and no write at all. A script
// cannot fail any of those, so it cannot pass them either.
//
// ── What is asserted, and what is not ──────────────────────────────────
// Persisted state, capability calls and receipts. The model's sentences
// are logged for a human to read and are never the evidence: the whole
// point of the receipt is that prose is not proof.
func TestLedgerLiveMonthlyCommitment(t *testing.T) {
	t.Run("reads the month and writes nothing", func(t *testing.T) {
		l := newLiveEnv(t)
		l.grantMonthlyCommitment(t)
		t.Logf("model: %s", l.model)
		cat := l.seedCategory(l.wsA, "Casa", domain.EntryTypeExpense)
		rent := l.seedLiveRecurring(t, cat, "Aluguel", 250000, 5, false)
		l.seedLiveRecurring(t, cat, "Academia", 17000, 10, false)

		// One bill already settled, so "o que falta" has a real answer that
		// is not simply "everything".
		l.liveMonth(t) // materialise
		if _, err := l.svc.MarkOccurrencePaid(ctxFor(l.wsA), app.OccurrenceRef{
			WorkspaceID: l.wsA, RecurringEntryID: rent.ID,
		}); err != nil {
			t.Fatalf("settle the rent: %v", err)
		}

		before := l.liveMonth(t)
		sink := l.say(t, "O que falta pagar esse mês?")

		if !called(sink, RecurringMonthTool) {
			t.Fatal("a question about the month was answered without reading the month")
		}
		for _, w := range []chatdomain.ToolName{MarkPaidTool, MarkPendingTool, SetMonthAmountTool} {
			if called(sink, w) {
				t.Fatalf("a question caused %s to run", w)
			}
		}
		after := l.liveMonth(t)
		if after.Totals.PaidCents != before.Totals.PaidCents ||
			after.Totals.CommittedCents != before.Totals.CommittedCents {
			t.Fatal("a question changed the month")
		}
		// The figure in the answer has to be the figure in the database.
		// Checked loosely, on the one number that matters: what is left.
		if !strings.Contains(sink.text.String(), "170,00") &&
			!strings.Contains(sink.text.String(), "170") {
			t.Errorf("the answer does not contain the remaining amount:\n%s", sink.text.String())
		}
	})

	t.Run("settles exactly one bill", func(t *testing.T) {
		l := newLiveEnv(t)
		l.grantMonthlyCommitment(t)
		cat := l.seedCategory(l.wsA, "Casa", domain.EntryTypeExpense)
		internet := l.seedLiveRecurring(t, cat, "Internet", 13000, 15, false)
		rent := l.seedLiveRecurring(t, cat, "Aluguel", 250000, 5, false)
		l.liveMonth(t)

		messageID, sink := l.sendAndCapture(t, "Paguei a internet.")

		// The month is read BEFORE the write: the id a write targets comes
		// from that read, and a model that wrote without one guessed it.
		if !called(sink, RecurringMonthTool) {
			t.Error("the bill was settled without reading the month first")
		}
		if got := l.occurrenceOf(t, internet.ID); got.Status != domain.OccurrencePaid {
			t.Fatalf("the internet was not settled: %q", got.Status)
		}
		if got := l.occurrenceOf(t, rent.ID); got.Status != domain.OccurrencePending {
			t.Fatal("settling the internet also settled the rent")
		}
		// Exactly one write, proved by the receipt rather than by counting
		// what the model said it did.
		r := l.receiptFor(t, messageID)
		if r.Executed != 1 || r.Failed != 0 || r.Refused != 0 {
			t.Fatalf("receipt: executed=%d failed=%d refused=%d", r.Executed, r.Failed, r.Refused)
		}
		if len(r.Writes) != 1 || r.Writes[0].Capability != MarkPaidTool {
			t.Fatalf("the receipt names %v", r.Writes)
		}
		// And nothing reached the ledger.
		if n := l.liveCount(l.wsA); n != 0 {
			t.Fatalf("settling a bill created %d transactions", n)
		}
	})

	t.Run("reverses a mistaken settlement", func(t *testing.T) {
		l := newLiveEnv(t)
		l.grantMonthlyCommitment(t)
		cat := l.seedCategory(l.wsA, "Casa", domain.EntryTypeExpense)
		internet := l.seedLiveRecurring(t, cat, "Internet", 13000, 15, false)
		l.liveMonth(t)

		l.say(t, "Paguei a internet.")
		if got := l.occurrenceOf(t, internet.ID); got.Status != domain.OccurrencePaid {
			t.Skip("the settlement did not happen, so there is nothing to reverse")
		}

		messageID, _ := l.sendAndCapture(t, "Marquei errado. A internet ainda não foi paga.")
		if got := l.occurrenceOf(t, internet.ID); got.Status != domain.OccurrencePending {
			t.Fatalf("the reversal left it %q", got.Status)
		}
		r := l.receiptFor(t, messageID)
		if r.Executed != 1 {
			t.Fatalf("receipt: executed=%d, want exactly 1", r.Executed)
		}
		if len(r.Writes) != 1 || r.Writes[0].Capability != MarkPendingTool {
			t.Fatalf("the receipt names %v", r.Writes)
		}
	})

	// ── The one that matters most ──────────────────────────────────
	//
	// Two obligations either of which a person could mean by "a internet".
	// The right behaviour is a QUESTION and no write: settling the wrong
	// bill leaves the real one looking paid, and nothing in the system will
	// ever flag it.
	t.Run("asks rather than guessing when two bills could be meant", func(t *testing.T) {
		l := newLiveEnv(t)
		l.grantMonthlyCommitment(t)
		cat := l.seedCategory(l.wsA, "Casa", domain.EntryTypeExpense)
		home := l.seedLiveRecurring(t, cat, "Internet casa", 13000, 15, false)
		office := l.seedLiveRecurring(t, cat, "Internet escritório", 19900, 20, false)
		l.liveMonth(t)

		messageID, sink := l.sendAndCapture(t, "Paguei a internet.")

		for _, f := range []*domain.RecurringEntry{home, office} {
			if got := l.occurrenceOf(t, f.ID); got.Status != domain.OccurrencePending {
				t.Fatalf("an ambiguous instruction settled %q", f.Description)
			}
		}
		r := l.receiptFor(t, messageID)
		if r.Executed != 0 {
			t.Fatalf("an ambiguous instruction executed %d writes", r.Executed)
		}
		// Having read the month is not required, but asking is: the answer
		// has to come back as a question the user can answer.
		if answer := sink.text.String(); !strings.Contains(answer, "?") {
			t.Errorf("the model did not ask which bill was meant:\n%s", answer)
		}
	})

	t.Run("records a variable amount without settling it", func(t *testing.T) {
		l := newLiveEnv(t)
		l.grantMonthlyCommitment(t)
		cat := l.seedCategory(l.wsA, "Casa", domain.EntryTypeExpense)
		power := l.seedLiveRecurring(t, cat, "Luz", 42000, 25, true)
		l.liveMonth(t)

		messageID, _ := l.sendAndCapture(t, "A luz desse mês veio 437,20.")

		got := l.occurrenceOf(t, power.ID)
		if got.AmountCents != 43720 {
			t.Errorf("the month holds %d cents, want 43720", got.AmountCents)
		}
		if got.AmountEstimated {
			t.Error("a confirmed amount is still marked an estimate")
		}
		// Saying what it cost is not saying it was paid.
		if got.Status != domain.OccurrencePending {
			t.Errorf("recording the amount also settled the bill: %q", got.Status)
		}
		r := l.receiptFor(t, messageID)
		if r.Executed != 1 {
			t.Errorf("receipt: executed=%d, want exactly 1", r.Executed)
		}
		if len(r.Writes) == 1 && r.Writes[0].Capability != SetMonthAmountTool {
			t.Errorf("the receipt names %v", r.Writes)
		}
	})
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
