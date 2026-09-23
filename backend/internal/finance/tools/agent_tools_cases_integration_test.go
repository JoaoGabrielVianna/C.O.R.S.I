//go:build integration

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

/* ══ 1. the catalogue and the default ═════════════════════════════════ */

// Every capability is offered, and none of them is permission.
func TestFinanceCapabilitiesAreInTheCatalogue(t *testing.T) {
	e := newEnv(t)
	for _, name := range allFinanceTools {
		tool, ok := e.registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not in the production catalogue", name)
		}
		def := tool.Definition()
		if err := def.Validate(); err != nil {
			t.Fatalf("%s has an invalid definition: %v", name, err)
		}
		if def.Internal {
			t.Fatalf("%s is marked internal; it is a product capability", name)
		}
	}

	// The three writes declare themselves as writes. The interface uses
	// this field to warn a person before they authorize, and the model is
	// told the same thing on the wire — a capability that lied here would
	// slip past whatever an operator decides a write deserves.
	writes := map[chatdomain.ToolName]bool{
		TransactionCreateTool: true, TransactionUpdateTool: true, TransactionDeleteTool: true,
	}
	for _, name := range allFinanceTools {
		tool, _ := e.registry.Lookup(name)
		want := chatdomain.EffectRead
		if writes[name] {
			want = chatdomain.EffectWrite
		}
		if got := tool.Definition().Effect; got != want {
			t.Errorf("%s declares effect %q, want %q", name, got, want)
		}
	}
}

// A fresh agent holds nothing, and being CALLED Ledger changes nothing.
func TestFreshAgentHasNoFinanceGrantsEvenWhenNamedLedger(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "Ledger")

	report, err := e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agentID)
	if err != nil {
		t.Fatalf("agent tools: %v", err)
	}
	if report.AuthorizedCount != 0 {
		t.Fatalf("a fresh agent holds %d grants, want 0", report.AuthorizedCount)
	}

	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	before := e.liveCount(e.wsA)

	// The write is attempted through the real loop, by an agent whose name
	// is the one the product will use. Nothing about the name is consulted
	// anywhere, and this is what says so.
	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+cat.ID.String()+`","amount_cents":8990,"description":"mercado"}`)
	sink := e.turn(e.wsA, convID, "registra 89,90 de mercado")

	ev, ok := sink.finished(TransactionCreateTool)
	if !ok {
		t.Fatal("no terminal tool event")
	}
	// A refusal is not_executed: the capability never ran. The live frame
	// carries the outcome's own status, so it says the same thing the audit
	// row does.
	if ev.Status != string(chatdomain.ToolCallNotExecuted) || ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("call ended %q/%q, want error/tool_not_authorized", ev.Status, ev.ErrorCode)
	}
	if after := e.liveCount(e.wsA); after != before {
		t.Fatalf("an unauthorized call created %d rows", after-before)
	}
}

// Each grant admits exactly one capability, and revoking removes it.
func TestGrantsAreIndividualAndRevocable(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "Reader")
	e.authorize(e.wsA, agentID, SummaryGetTool)

	report, _ := e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agentID)
	if report.AuthorizedCount != 1 {
		t.Fatalf("granted one capability, agent holds %d", report.AuthorizedCount)
	}
	for _, item := range report.Items {
		if item.Name == SummaryGetTool && !item.Authorized {
			t.Fatal("the granted capability is not authorized")
		}
		if item.Name != SummaryGetTool && item.Authorized {
			t.Fatalf("%s became authorized without being granted", item.Name)
		}
	}

	e.revoke(e.wsA, agentID, SummaryGetTool)
	report, _ = e.chatSvc.AgentTools(ctxFor(e.wsA), e.wsA, agentID)
	if report.AuthorizedCount != 0 {
		t.Fatalf("after revoke the agent still holds %d grants", report.AuthorizedCount)
	}
}

// Revoking takes effect on the very next turn of a conversation already
// under way — the grant is re-read per turn, never cached from the one that
// declared the tools.
func TestRevokeTakesEffectMidConversation(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allFinanceTools...)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+cat.ID.String()+`","amount_cents":5800,"description":"uber"}`)
	first := e.turn(e.wsA, convID, "registra 58 de uber")
	if ev, _ := first.finished(TransactionCreateTool); ev.Status != "ok" {
		t.Fatalf("authorized call ended %q", ev.Status)
	}
	created := e.liveCount(e.wsA)

	e.revoke(e.wsA, agentID, TransactionCreateTool)

	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+cat.ID.String()+`","amount_cents":4200,"description":"uber 2"}`)
	second := e.turn(e.wsA, convID, "registra mais um de 42")
	ev, ok := second.finished(TransactionCreateTool)
	if !ok {
		t.Fatal("no terminal tool event on the second turn")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("after revoke the call ended %q/%q", ev.Status, ev.ErrorCode)
	}
	if now := e.liveCount(e.wsA); now != created {
		t.Fatalf("a revoked capability wrote %d rows", now-created)
	}
}

/* ══ 2. money ═════════════════════════════════════════════════════════ */

// The number the model sends is the number Postgres stores. Read back in
// SQL, not through the service that wrote it.
func TestAmountReachesPostgresExactly(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	cases := []struct {
		cents     int64
		rendering string
	}{
		{8990, "R$ 89,90"}, // the sprint's own example
		{9000, "R$ 90,00"}, // a round figure said as "90"
		{1, "R$ 0,01"},     // the smallest unit
		{99, "R$ 0,99"},    // under one real
		{125000, "R$ 1.250,00"},
		{1100000, "R$ 11.000,00"}, // "recebi 11 mil"
		{999999999, "R$ 9.999.999,99"},
	}
	for _, c := range cases {
		out := e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
			"category_id": cat.ID.String(), "amount_cents": c.cents, "description": "fixture",
		})
		id := uuid.MustParse(str(t, out, "transaction_id"))

		stored, ok := e.rawAmount(id)
		if !ok {
			t.Fatalf("%d: row not found in Postgres", c.cents)
		}
		if stored != c.cents {
			t.Fatalf("sent %d cents, Postgres holds %d", c.cents, stored)
		}
		// Both fields of the echo, and both derived from what was stored.
		if got := num(t, out, "amount_cents"); got != c.cents {
			t.Fatalf("echoed amount_cents %d, want %d", got, c.cents)
		}
		if got := str(t, out, "amount"); got != c.rendering {
			t.Fatalf("echoed amount %q, want %q", got, c.rendering)
		}
	}
}

// A decimal amount is refused by the schema before any executor runs.
//
// This is the load-bearing consequence of declaring amount_cents as an
// INTEGER: the model that writes 89.90 — the single likeliest money mistake
// there is — gets an actionable failure instead of a row holding 89 cents.
func TestDecimalAmountIsRefusedAndWritesNothing(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	before := e.liveCount(e.wsA)

	for _, raw := range []string{"89.90", "0.5", "8990.0001", `"8990"`} {
		_, err := e.executeRaw(e.wsA, TransactionCreateTool,
			`{"category_id":"`+cat.ID.String()+`","amount_cents":`+raw+`,"description":"x"}`)
		if err == nil {
			t.Fatalf("amount_cents %s was accepted", raw)
		}
		if code := toolCode(err); code != chatdomain.ToolErrInvalidArguments {
			t.Fatalf("amount_cents %s failed with %q, want invalid arguments", raw, code)
		}
	}
	// 8990.0 is an integer written with a fractional part of zero, and JSON
	// makes no distinction; it is accepted and stores 8990, which is right.
	if after := e.liveCount(e.wsA); after != before {
		t.Fatalf("a refused amount created %d rows", after-before)
	}
}

// A negative or zero amount is refused by the domain, not by the tool: the
// direction of money is the category's job, and the amount is a magnitude.
func TestNonPositiveAmountIsRefusedByTheDomain(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	for _, cents := range []int64{0, -1, -8990} {
		_, err := e.executeErr(e.wsA, TransactionCreateTool, map[string]any{
			"category_id": cat.ID.String(), "amount_cents": cents, "description": "x",
		})
		if err == nil {
			t.Fatalf("amount_cents %d was accepted", cents)
		}
	}
}

/* ══ 3. the conversation, end to end ══════════════════════════════════ */

// The sprint's first three scenarios, in one conversation, through the real
// turn loop: record, read, correct.
func TestConversationRecordsReadsAndCorrects(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allFinanceTools...)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	/* ── "Registra R$ 89,90 de mercado hoje." ───────────────────────── */
	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+cat.ID.String()+`","amount_cents":8990,"description":"mercado"}`)
	sink := e.turn(e.wsA, convID, "registra R$ 89,90 de mercado hoje")
	if ev, _ := sink.finished(TransactionCreateTool); ev.Status != "ok" {
		t.Fatalf("create ended %q: %s", ev.Status, ev.ErrorCode)
	}
	if n := e.liveCount(e.wsA); n != 1 {
		t.Fatalf("after one create there are %d live rows", n)
	}

	// Find it the way the agent would.
	listed := e.execute(t, e.wsA, TransactionListTool, map[string]any{"period": "today"})
	items := rows(t, listed, "transactions")
	if len(items) != 1 {
		t.Fatalf("today lists %d rows, want 1", len(items))
	}
	id := uuid.MustParse(str(t, items[0], "id"))

	// It is an EXPENSE, and nothing said so: the direction came from the
	// category, which is the one place it can come from.
	if got := str(t, items[0], "type"); got != string(domain.EntryTypeExpense) {
		t.Fatalf("type is %q, want expense", got)
	}
	// And it is marked as agent-written, from the domain's own enum.
	source, deleted, ok := e.rawRow(id)
	if !ok {
		t.Fatal("row missing")
	}
	if source != string(domain.TransactionSourceAI) {
		t.Fatalf("source is %q, want ai — the origin badge would be lying", source)
	}
	if deleted != nil {
		t.Fatal("a freshly created row is soft-deleted")
	}

	/* ── "Quanto gastei hoje?" ──────────────────────────────────────── */
	summary := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "today"})
	realized := nested(t, summary, "realized")
	if got := num(t, realized, "expense_cents"); got != 8990 {
		t.Fatalf("today's realized expense is %d cents, want 8990", got)
	}
	if got := str(t, realized, "expense"); got != "R$ 89,90" {
		t.Fatalf("today's realized expense renders as %q", got)
	}
	// The net is computed here rather than left to prose arithmetic.
	if got := num(t, realized, "net_cents"); got != -8990 {
		t.Fatalf("net is %d, want -8990", got)
	}
	// The window says which days it covered, and what today is — the only
	// place in a turn where the date is stated at all.
	window := nested(t, summary, "window")
	todayStr := str(t, summary, "today")
	if str(t, window, "from") != todayStr || str(t, window, "to") != todayStr {
		t.Fatalf("the 'today' window is %v but today is %s", window, todayStr)
	}
	if str(t, window, "time_zone") != "America/Sao_Paulo" {
		t.Fatalf("window zone is %q", str(t, window, "time_zone"))
	}

	/* ── "Na verdade eram R$ 79,90." ────────────────────────────────── */
	e.llm.scriptToolCall(TransactionUpdateTool,
		`{"transaction_id":"`+id.String()+`","amount_cents":7990}`)
	sink = e.turn(e.wsA, convID, "na verdade eram 79,90")
	if ev, _ := sink.finished(TransactionUpdateTool); ev.Status != "ok" {
		t.Fatalf("update ended %q: %s", ev.Status, ev.ErrorCode)
	}

	// The SAME row moved. A correction that created a second transaction
	// would leave the totals double-counted and both figures defensible.
	if n := e.liveCount(e.wsA); n != 1 {
		t.Fatalf("after a correction there are %d live rows, want 1", n)
	}
	stored, _ := e.rawAmount(id)
	if stored != 7990 {
		t.Fatalf("Postgres holds %d cents after the correction, want 7990", stored)
	}
	summary = e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "today"})
	if got := num(t, nested(t, summary, "realized"), "expense_cents"); got != 7990 {
		t.Fatalf("the summary still reports %d cents", got)
	}

	/* ── "Essa transação era teste, pode apagar." ───────────────────── */
	e.llm.scriptToolCall(TransactionDeleteTool, `{"transaction_id":"`+id.String()+`"}`)
	sink = e.turn(e.wsA, convID, "era teste, pode apagar")
	if ev, _ := sink.finished(TransactionDeleteTool); ev.Status != "ok" {
		t.Fatalf("delete ended %q: %s", ev.Status, ev.ErrorCode)
	}
	if n := e.liveCount(e.wsA); n != 0 {
		t.Fatalf("after the delete there are %d live rows", n)
	}
	// The canonical removal is a SOFT delete — the same one the screens
	// perform. The row is still there and stamped; there is no second
	// removal semantics for agents.
	_, deletedAt, ok := e.rawRow(id)
	if !ok {
		t.Fatal("the row was hard-deleted; Finance soft-deletes")
	}
	if deletedAt == nil {
		t.Fatal("deleted_at was not stamped")
	}
	// And it is gone from every read.
	if _, err := e.executeErr(e.wsA, TransactionGetTool, map[string]any{"transaction_id": id.String()}); err == nil {
		t.Fatal("a deleted transaction is still readable")
	}
	summary = e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "today"})
	if got := num(t, nested(t, summary, "realized"), "expense_cents"); got != 0 {
		t.Fatalf("a deleted transaction still contributes %d cents", got)
	}
}

// The conversation, its turns and its audit survive a reload: nothing about
// the state above lives in the process that produced it.
func TestConversationAndAuditSurviveReload(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allFinanceTools...)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+cat.ID.String()+`","amount_cents":8990,"description":"mercado"}`)
	e.turn(e.wsA, convID, "registra 89,90 de mercado")

	// Read the thread back the way opening the conversation does.
	conv, err := e.chatSvc.GetConversation(ctxFor(e.wsA), e.wsA, convID)
	if err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if conv.AgentID != agentID {
		t.Fatal("the reloaded conversation lost its agent")
	}
	messages, err := e.chatSvc.ListMessages(ctxFor(e.wsA), e.wsA, convID, 0)
	if err != nil {
		t.Fatalf("reload messages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("the reloaded thread has %d messages", len(messages))
	}

	// The audit trail is the EXISTING chat.tool_calls mechanism. No second
	// log was created for Finance, and this is what says so.
	var n int
	var name, status string
	var args, result *string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.tool_calls WHERE conversation_id = $1`, convID).Scan(&n); err != nil {
		t.Fatalf("count tool calls: %v", err)
	}
	if n != 1 {
		t.Fatalf("chat.tool_calls holds %d rows for this conversation, want 1", n)
	}
	if err := e.pool.QueryRow(context.Background(),
		`SELECT tool_name, status, arguments, result FROM chat.tool_calls WHERE conversation_id = $1`,
		convID).Scan(&name, &status, &args, &result); err != nil {
		t.Fatalf("read tool call: %v", err)
	}
	if name != string(TransactionCreateTool) || status != "ok" {
		t.Fatalf("audit row is %s/%s", name, status)
	}
	// The payload is deliberately NOT here: Finance capabilities declare
	// themselves confidential, so the trail keeps which tool ran and how it
	// ended and drops what it carried. Proven in full by
	// TestFinancePayloadsAreRedactedInTheAuditTrail.
	if args != nil || result != nil {
		t.Fatalf("a confidential call persisted its payload: args=%v result=%v", args, result)
	}
}

// A failing tool is recorded as a failure and reported as one. It never
// becomes a success the model can narrate.
func TestToolFailureIsRecordedAsFailure(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allFinanceTools...)

	// A category id that resolves to nothing.
	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+uuid.New().String()+`","amount_cents":8990,"description":"mercado"}`)
	sink := e.turn(e.wsA, convID, "registra 89,90")

	ev, ok := sink.finished(TransactionCreateTool)
	if !ok {
		t.Fatal("no terminal event")
	}
	if ev.Status != "error" {
		t.Fatalf("a failing call ended %q", ev.Status)
	}
	if e.liveCount(e.wsA) != 0 {
		t.Fatal("a failing create wrote a row")
	}

	var status, code string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status, error_code FROM chat.tool_calls WHERE conversation_id = $1`, convID).
		Scan(&status, &code); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if status != "error" || code == "" {
		t.Fatalf("the audit row says %s/%q", status, code)
	}
}

/* ══ 4. reading ═══════════════════════════════════════════════════════ */

// The questions the sprint listed, answered from the capabilities rather
// than by pulling the table into the prompt.
func TestReadCapabilitiesAnswerTheRealQuestions(t *testing.T) {
	e := newEnv(t)
	mercado := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	transporte := e.seedCategory(e.wsA, "Transporte", domain.EntryTypeExpense)
	salario := e.seedCategory(e.wsA, "Salário", domain.EntryTypeIncome)

	e.seedTx(e.wsA, mercado, 8990, "mercado do mês", e.daysAgo(1))
	e.seedTx(e.wsA, mercado, 4500, "padaria", e.daysAgo(2))
	e.seedTx(e.wsA, transporte, 5800, "Uber para o aeroporto", e.daysAgo(3))
	e.seedTx(e.wsA, transporte, 19900, "passagem", e.daysAgo(4))
	e.seedTx(e.wsA, salario, 1100000, "salário", e.daysAgo(5))

	// "Quanto entrou, quanto saiu, e qual o líquido."
	sum := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "last_30_days"})
	realized := nested(t, sum, "realized")
	if got := num(t, realized, "income_cents"); got != 1100000 {
		t.Fatalf("income %d", got)
	}
	if got := num(t, realized, "expense_cents"); got != 8990+4500+5800+19900 {
		t.Fatalf("expense %d", got)
	}
	if got := num(t, realized, "net_cents"); got != 1100000-(8990+4500+5800+19900) {
		t.Fatalf("net %d", got)
	}

	// "Quais categorias estão pesando mais?" — ordered by total, named.
	byCat := rows(t, sum, "by_category")
	if len(byCat) < 3 {
		t.Fatalf("by_category has %d entries", len(byCat))
	}
	if str(t, byCat[0], "category") != "Salário" {
		t.Fatalf("the largest bucket is %q", str(t, byCat[0], "category"))
	}
	// The largest EXPENSE category is Transporte (25700 vs 13490).
	var firstExpense map[string]any
	for _, row := range byCat {
		if str(t, row, "type") == "expense" {
			firstExpense = row
			break
		}
	}
	if firstExpense == nil || str(t, firstExpense, "category") != "Transporte" {
		t.Fatalf("the heaviest expense category is %v", firstExpense)
	}

	// "Quanto gastei com transporte?" — the same figure, filtered.
	filtered := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "category_id": transporte.ID.String(),
	})
	if got := len(rows(t, filtered, "transactions")); got != 2 {
		t.Fatalf("transporte lists %d rows", got)
	}

	// "Quais foram minhas maiores despesas?" — ordered by amount.
	largest := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "type": "expense", "order": "largest", "limit": 2,
	})
	top := rows(t, largest, "transactions")
	if len(top) != 2 {
		t.Fatalf("asked for 2, got %d", len(top))
	}
	if num(t, top[0], "amount_cents") != 19900 || num(t, top[1], "amount_cents") != 8990 {
		t.Fatalf("largest-first returned %d then %d",
			num(t, top[0], "amount_cents"), num(t, top[1], "amount_cents"))
	}

	// "Tenho transações de R$ 199?" — an amount range.
	around := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "min_amount_cents": 19800, "max_amount_cents": 20000,
	})
	found := rows(t, around, "transactions")
	if len(found) != 1 || str(t, found[0], "description") != "passagem" {
		t.Fatalf("the amount range matched %v", found)
	}

	// "Aquela compra da Amazon" — free text on the description.
	searched := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "search": "uber",
	})
	hits := rows(t, searched, "transactions")
	if len(hits) != 1 || str(t, hits[0], "description") != "Uber para o aeroporto" {
		t.Fatalf("search matched %v", hits)
	}

	// A search term full of LIKE metacharacters matches literally rather
	// than matching everything.
	noise := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "search": "%",
	})
	if got := len(rows(t, noise, "transactions")); got != 0 {
		t.Fatalf("searching for %% matched %d rows; the wildcard was not escaped", got)
	}
}

// A listing that had to stop says so, so nothing downstream mistakes a page
// for a total.
func TestListingDeclaresThatMoreExists(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	for i := 0; i < 5; i++ {
		e.seedTx(e.wsA, cat, int64(100*(i+1)), "row", e.daysAgo(i))
	}
	page := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "limit": 2,
	})
	if len(rows(t, page, "transactions")) != 2 {
		t.Fatal("limit was not honoured")
	}
	if more, _ := page["has_more"].(bool); !more {
		t.Fatal("a partial page did not declare that more exists")
	}
	full := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "limit": 50,
	})
	if more, _ := full["has_more"].(bool); more {
		t.Fatal("a complete page claimed more exists")
	}
}

// Categories are the vocabulary a write must choose from, and the tool
// offers no way to invent one.
func TestCategoryListIsTheWholeVocabulary(t *testing.T) {
	e := newEnv(t)
	empty := e.execute(t, e.wsA, CategoryListTool, nil)
	if len(rows(t, empty, "categories")) != 0 {
		t.Fatal("a fresh workspace has categories")
	}
	if _, ok := empty["note"]; !ok {
		t.Fatal("an empty category list does not explain itself")
	}

	e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	e.seedCategory(e.wsA, "Salário", domain.EntryTypeIncome)

	all := e.execute(t, e.wsA, CategoryListTool, nil)
	if got := len(rows(t, all, "categories")); got != 2 {
		t.Fatalf("listed %d categories", got)
	}
	income := e.execute(t, e.wsA, CategoryListTool, map[string]any{"type": "income"})
	only := rows(t, income, "categories")
	if len(only) != 1 || str(t, only[0], "name") != "Salário" {
		t.Fatalf("income filter returned %v", only)
	}
}

// TestCategoryListCarriesTheDateAnchor is the regression for a relative
// date arriving at a write with nothing to count from.
//
// ── The turn this protects ─────────────────────────────────────────────
// "Ontem gastei 90 no cinema". The model is instructed to choose a category
// before recording, so the turn is category.list → transaction.create, and
// for a long time the first of those returned no date. The model reached
// the write with no anchor and two ways to be wrong: omit occurred_on and
// file yesterday's money under today, or compose a date out of its
// training. Both produce a real row on the wrong day, and neither raises an
// error anywhere.
//
// The assertion is deliberately about the FRESH workspace as well: a
// listing with no categories in it still has to carry the date, because the
// date is a fact about the clock rather than about the list.
func TestCategoryListCarriesTheDateAnchor(t *testing.T) {
	e := newEnv(t)

	want := e.today().Format("2006-01-02")

	empty := e.execute(t, e.wsA, CategoryListTool, nil)
	if got := str(t, empty, "today"); got != want {
		t.Fatalf("an empty category listing reported today as %q, want %q", got, want)
	}
	if got := str(t, empty, "time_zone"); got != e.loc.String() {
		t.Fatalf("the listing reported the zone as %q, want %q", got, e.loc.String())
	}

	e.seedCategory(e.wsA, "Lazer", domain.EntryTypeExpense)
	full := e.execute(t, e.wsA, CategoryListTool, nil)
	if got := str(t, full, "today"); got != want {
		t.Fatalf("a populated category listing reported today as %q, want %q", got, want)
	}

	// The filtered listing is the same read and must answer the same way:
	// a model that narrowed to expenses has not stopped needing the date.
	filtered := e.execute(t, e.wsA, CategoryListTool, map[string]any{"type": "expense"})
	if got := str(t, filtered, "today"); got != want {
		t.Fatalf("a filtered category listing reported today as %q, want %q", got, want)
	}
}

// TestRelativeDateFromTheAnchorIsStoredOnThatDay walks the anchor all the
// way to the stored column.
//
// It is the deterministic half of the live-model claim: given the date
// category.list reports, counting one day back and sending it as
// occurred_on puts the row on yesterday and NOT on today. The live suite
// proves a real model does the counting; this proves the counting lands
// where it should.
func TestRelativeDateFromTheAnchorIsStoredOnThatDay(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Lazer", domain.EntryTypeExpense)

	anchor := str(t, e.execute(t, e.wsA, CategoryListTool, nil), "today")
	day, err := time.ParseInLocation("2006-01-02", anchor, e.loc)
	if err != nil {
		t.Fatalf("the anchor %q is not a calendar date: %v", anchor, err)
	}
	yesterday := day.AddDate(0, 0, -1).Format("2006-01-02")

	out := e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
		"category_id":  cat.ID.String(),
		"amount_cents": 9000,
		"description":  "cinema",
		"occurred_on":  yesterday,
	})
	if got := str(t, out, "occurred_on"); got != yesterday {
		t.Fatalf("the create reported %q, want %q", got, yesterday)
	}

	id, err := uuid.Parse(str(t, out, "transaction_id"))
	if err != nil {
		t.Fatalf("transaction_id: %v", err)
	}
	var stored time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT occurred_at FROM finance.transactions WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := stored.In(e.loc).Format("2006-01-02"); got != yesterday {
		t.Fatalf("the stored row sits on %s, want %s", got, yesterday)
	}
}

// Money that has not moved is reported apart from money that has.
func TestScheduledMoneyIsProjectedAndNotRealized(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Contas", domain.EntryTypeExpense)

	// One paid today, one scheduled for a future day inside this month's
	// window — or next month's, if today is the last day; the assertion
	// uses whichever window contains both.
	out := e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 5000, "description": "luz",
	})
	_ = out
	future := e.today().AddDate(0, 0, 3)
	if _, err := e.svc.CreateTransaction(ctxFor(e.wsA), app.CreateTransactionInput{
		WorkspaceID: e.wsA, CategoryID: cat.ID, AmountCents: 12000,
		Description: "internet", OccurredAt: future,
		Status: statusPtr(domain.TransactionStatusScheduled),
	}); err != nil {
		t.Fatalf("seed scheduled: %v", err)
	}

	// An explicit window, because every named period stops at today and the
	// scheduled row is deliberately in the future — which is the whole
	// point of the distinction being tested.
	sum := e.execute(t, e.wsA, SummaryGetTool, map[string]any{
		"from": e.daysAgo(30).Format("2006-01-02"),
		"to":   e.today().AddDate(0, 0, 30).Format("2006-01-02"),
	})
	if got := num(t, nested(t, sum, "realized"), "expense_cents"); got != 5000 {
		t.Fatalf("realized expense is %d, want only the paid 5000", got)
	}
	if got := num(t, nested(t, sum, "projected"), "expense_cents"); got != 12000 {
		t.Fatalf("projected expense is %d, want the scheduled 12000", got)
	}
}

func statusPtr(s domain.TransactionStatus) *domain.TransactionStatus { return &s }

/* ══ 5. workspace isolation ═══════════════════════════════════════════ */

// Workspace B can neither read nor change workspace A's money, and cannot
// learn from the refusal that the row exists.
func TestWorkspaceIsolationAndNoExistenceLeak(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	tx := e.seedTx(e.wsA, cat, 8990, "mercado", e.today())

	// Read.
	if _, err := e.executeErr(e.wsB, TransactionGetTool, map[string]any{
		"transaction_id": tx.ID.String(),
	}); err == nil {
		t.Fatal("workspace B read workspace A's transaction")
	} else if code := toolCode(err); code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("cross-workspace read failed with %q", code)
	}

	// The message for an id belonging to someone else must be the SAME as
	// for one that never existed. A difference would be a probe.
	_, realErr := e.executeErr(e.wsB, TransactionGetTool, map[string]any{
		"transaction_id": tx.ID.String(),
	})
	_, fakeErr := e.executeErr(e.wsB, TransactionGetTool, map[string]any{
		"transaction_id": uuid.New().String(),
	})
	if realErr.Error() != fakeErr.Error() {
		t.Fatalf("a real id in another workspace answers %q while a fabricated one answers %q",
			realErr, fakeErr)
	}

	// Write.
	if _, err := e.executeErr(e.wsB, TransactionUpdateTool, map[string]any{
		"transaction_id": tx.ID.String(), "amount_cents": 1,
	}); err == nil {
		t.Fatal("workspace B updated workspace A's transaction")
	}
	if _, err := e.executeErr(e.wsB, TransactionDeleteTool, map[string]any{
		"transaction_id": tx.ID.String(),
	}); err == nil {
		t.Fatal("workspace B deleted workspace A's transaction")
	}
	stored, _ := e.rawAmount(tx.ID)
	if stored != 8990 {
		t.Fatalf("workspace A's row now holds %d cents", stored)
	}
	if e.liveCount(e.wsA) != 1 {
		t.Fatal("workspace A's row is gone")
	}

	// B's reads see nothing of A's.
	listed := e.execute(t, e.wsB, TransactionListTool, map[string]any{"period": "last_30_days"})
	if got := len(rows(t, listed, "transactions")); got != 0 {
		t.Fatalf("workspace B lists %d of workspace A's rows", got)
	}
	sum := e.execute(t, e.wsB, SummaryGetTool, map[string]any{"period": "last_30_days"})
	if got := num(t, nested(t, sum, "realized"), "expense_cents"); got != 0 {
		t.Fatalf("workspace B's summary includes %d cents of workspace A's money", got)
	}
	cats := e.execute(t, e.wsB, CategoryListTool, nil)
	if got := len(rows(t, cats, "categories")); got != 0 {
		t.Fatalf("workspace B sees %d of workspace A's categories", got)
	}

	// Writing into B with A's category id is refused: the category is
	// resolved inside the workspace before anything is inserted.
	if _, err := e.executeErr(e.wsB, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 100, "description": "x",
	}); err == nil {
		t.Fatal("workspace B wrote a row against workspace A's category")
	}
}

// A call that arrives with no workspace at all is refused rather than
// defaulted.
func TestMissingWorkspaceIsRefused(t *testing.T) {
	e := newEnv(t)
	tool, _ := e.registry.Lookup(SummaryGetTool)
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("a call with no workspace produced an answer")
	}
}

/* ══ 6. the composite structures ══════════════════════════════════════ */

// A transfer leg keeps its pair invariant: the amount and the date cannot be
// changed through the agent, and the leg cannot be deleted on its own.
func TestTransferLegIsProtected(t *testing.T) {
	e := newEnv(t)
	from := e.seedCategory(e.wsA, "Saída", domain.EntryTypeExpense)
	to := e.seedCategory(e.wsA, "Entrada", domain.EntryTypeIncome)
	pair, err := e.svc.CreateTransfer(ctxFor(e.wsA), app.CreateTransferInput{
		WorkspaceID: e.wsA, FromCategoryID: from.ID, ToCategoryID: to.ID,
		AmountCents: 50000, Description: "entre contas", OccurredAt: e.today(),
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}

	// The amount is refused, and the refusal explains the structure.
	_, err = e.executeErr(e.wsA, TransactionUpdateTool, map[string]any{
		"transaction_id": pair.From.ID.String(), "amount_cents": 1,
	})
	if err == nil {
		t.Fatal("one leg of a transfer had its amount changed")
	}
	if !strings.Contains(err.Error(), "transfer") {
		t.Fatalf("the refusal does not explain why: %v", err)
	}
	if stored, _ := e.rawAmount(pair.From.ID); stored != 50000 {
		t.Fatalf("the leg now holds %d cents", stored)
	}

	// The description is not part of the invariant and stays editable.
	if _, err := e.executeErr(e.wsA, TransactionUpdateTool, map[string]any{
		"transaction_id": pair.From.ID.String(), "description": "transferência poupança",
	}); err != nil {
		t.Fatalf("a harmless edit to a transfer leg was refused: %v", err)
	}

	// Deleting a single leg is refused by the APPLICATION, and the model
	// receives the application's own sentence.
	_, err = e.executeErr(e.wsA, TransactionDeleteTool, map[string]any{
		"transaction_id": pair.From.ID.String(),
	})
	if err == nil {
		t.Fatal("one leg of a transfer was deleted on its own")
	}
	if !strings.Contains(err.Error(), "transfer pair") {
		t.Fatalf("the refusal does not name the pair: %v", err)
	}

	// And a listing flags what the row is, so the agent can explain before
	// being refused.
	listed := e.execute(t, e.wsA, TransactionListTool, map[string]any{"period": "today"})
	var flagged int
	for _, row := range rows(t, listed, "transactions") {
		if leg, _ := row["transfer_leg"].(bool); leg {
			flagged++
		}
	}
	if flagged != 2 {
		t.Fatalf("%d rows are flagged as transfer legs, want 2", flagged)
	}
}

// `transfer` is not offered as a payment method, and sending it anyway is
// refused with the reason.
func TestTransferIsNotAPaymentMethod(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	_, err := e.executeErr(e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 100,
		"description": "x", "payment_method": "transfer",
	})
	if err == nil {
		t.Fatal("a single row was created wearing payment_method=transfer")
	}
	if e.liveCount(e.wsA) != 0 {
		t.Fatal("a refused create wrote a row")
	}
}

/* ══ 7. context reference and hydration ═══════════════════════════════ */

// A reference identifies; it does not carry state, and it does not grant.
func TestReferenceIdentifiesWithoutGrantingOrCarryingState(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	tx := e.seedTx(e.wsA, cat, 8990, "mercado", e.today())

	agentID, _ := e.newAgent(e.wsA, "Ledger")
	convID := e.newConversationWith(e.wsA, agentID, chatdomain.ContextReference{
		Type: TransactionReferenceType, ID: tx.ID.String(),
	})

	conv, err := e.chatSvc.GetConversation(ctxFor(e.wsA), e.wsA, convID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	refs := conv.ContextReferences
	if len(refs) != 1 {
		t.Fatalf("the conversation holds %d references", len(refs))
	}
	// The label is the description — the only field whose job is
	// recognition.
	if refs[0].Label != "mercado" {
		t.Fatalf("label is %q, want the description", refs[0].Label)
	}
	// And the AMOUNT is nowhere in the persisted record. It is the thing
	// most likely to be corrected in this very conversation, and a stored
	// copy would keep the old figure in the model's context forever.
	if strings.Contains(refs[0].Label+refs[0].Subtitle, "89") {
		t.Fatalf("the persisted reference carries the amount: %q / %q", refs[0].Label, refs[0].Subtitle)
	}
	if refs[0].Subtitle != "" {
		t.Fatalf("subtitle is %q; a transaction has nothing stable to put there", refs[0].Subtitle)
	}

	// Attaching a row from another workspace is refused at admission.
	if _, err := e.chatSvc.CreateConversation(ctxFor(e.wsB), chatapp.CreateConversationInput{
		WorkspaceID: e.wsB, AgentID: agentID, Title: "t",
		ContextReferences: []chatdomain.ContextReference{
			{Type: TransactionReferenceType, ID: tx.ID.String()},
		},
	}); err == nil {
		t.Fatal("workspace B attached workspace A's transaction")
	}
}

// Hydration puts the CURRENT amount in front of the model, and only for an
// agent authorized to read it.
func TestHydrationIsFreshAndGatedOnTheReadGrant(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	tx := e.seedTx(e.wsA, cat, 8990, "mercado", e.today())

	agentID, _ := e.newAgent(e.wsA, "Ledger")
	convID := e.newConversationWith(e.wsA, agentID, chatdomain.ContextReference{
		Type: TransactionReferenceType, ID: tx.ID.String(),
	})

	// Without the read grant, nothing about the state reaches the prompt.
	e.llm.scriptReply("ok")
	e.turn(e.wsA, convID, "quanto foi essa?")
	if promptContains(e.llm, "89,90") {
		t.Fatal("an agent with no read grant received the amount through hydration")
	}

	// With it, the state arrives.
	e.authorize(e.wsA, agentID, TransactionGetTool)
	e.llm.scriptReply("ok")
	e.turn(e.wsA, convID, "quanto foi essa?")
	if !promptContains(e.llm, "89,90") {
		t.Fatal("an authorized agent did not receive the amount through hydration")
	}

	// The state is read FRESH each turn: correct the row, and the next turn
	// carries the new figure and not the old one.
	if _, err := e.svc.UpdateTransaction(ctxFor(e.wsA), app.UpdateTransactionInput{
		WorkspaceID: e.wsA, ID: tx.ID, AmountCents: int64Ptr(7990),
	}); err != nil {
		t.Fatalf("correct: %v", err)
	}
	e.llm.scriptReply("ok")
	e.turn(e.wsA, convID, "e agora?")
	if !promptContains(e.llm, "79,90") {
		t.Fatal("hydration did not carry the corrected amount")
	}
	if promptContains(e.llm, "89,90") {
		t.Fatal("hydration still carries the stale amount")
	}
}

func int64Ptr(v int64) *int64 { return &v }

// promptContains reports whether the last provider request carried the
// text anywhere in its messages.
func promptContains(f *fakeLLM, needle string) bool {
	if len(f.requests) == 0 {
		return false
	}
	req := f.requests[len(f.requests)-1]
	for _, m := range req.Messages {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}

/* ══ 8. ambiguity ═════════════════════════════════════════════════════ */

// When two rows could be the one the user meant, the capability returns
// both. Nothing here picks one, and nothing is changed by asking.
func TestAmbiguousMatchReturnsEveryCandidateAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	a := e.seedTx(e.wsA, cat, 8990, "mercado", e.daysAgo(1))
	b := e.seedTx(e.wsA, cat, 8990, "mercado", e.daysAgo(2))

	found := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"period": "last_30_days", "search": "mercado",
	})
	items := rows(t, found, "transactions")
	if len(items) != 2 {
		t.Fatalf("an ambiguous search returned %d rows, want both candidates", len(items))
	}
	// They are told apart by their dates, which is what the agent needs to
	// ask a useful question.
	if str(t, items[0], "occurred_on") == str(t, items[1], "occurred_on") {
		t.Fatal("the candidates are indistinguishable in the listing")
	}
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		if stored, _ := e.rawAmount(id); stored != 8990 {
			t.Fatalf("reading changed a row: %d", stored)
		}
	}
	if e.liveCount(e.wsA) != 2 {
		t.Fatal("reading changed the row count")
	}
}

/* ══ 9. dates ═════════════════════════════════════════════════════════ */

// An omitted date means now; a given one is honoured; and the row lands in
// the window the reporting zone says it should.
func TestDateHandlingAtTheBoundary(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	todayStr := time.Now().In(e.loc).Format("2006-01-02")
	out := e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 100, "description": "hoje",
	})
	if got := str(t, out, "occurred_on"); got != todayStr {
		t.Fatalf("an undated create landed on %s, want %s", got, todayStr)
	}

	yesterday := time.Now().In(e.loc).AddDate(0, 0, -1).Format("2006-01-02")
	out = e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 200,
		"description": "ontem", "occurred_on": yesterday,
	})
	if got := str(t, out, "occurred_on"); got != yesterday {
		t.Fatalf("a dated create landed on %s, want %s", got, yesterday)
	}

	// Each falls in its own window and not the other's.
	today := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "today"})
	if got := num(t, nested(t, today, "realized"), "expense_cents"); got != 100 {
		t.Fatalf("today's window holds %d cents, want 100", got)
	}
	yd := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "yesterday"})
	if got := num(t, nested(t, yd, "realized"), "expense_cents"); got != 200 {
		t.Fatalf("yesterday's window holds %d cents, want 200", got)
	}

	// A malformed date is refused rather than guessed at.
	if _, err := e.executeErr(e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 300,
		"description": "x", "occurred_on": "10/08/2026",
	}); err == nil {
		t.Fatal("a non-ISO date was accepted")
	}
}

// A window longer than the aggregation is allowed to span is refused by the
// application, and the refusal reaches the model as something it can fix.
func TestOversizedSummaryWindowIsRefused(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, SummaryGetTool, map[string]any{
		"from": "2020-01-01", "to": "2026-01-01",
	})
	if err == nil {
		t.Fatal("a six-year window was aggregated")
	}
	if code := toolCode(err); code != chatdomain.ToolErrInvalidArguments {
		t.Fatalf("failed with %q, want invalid arguments", code)
	}
}

// A transaction recorded as happening NOW is REALIZED immediately.
//
// ── The failure this pins down ─────────────────────────────────────────
// The realized/projected split is `status = 'paid' AND occurred_at <=
// now()`, evaluated by Postgres. When the tool stamped `occurred_at` from
// the API process's own clock, any skew in which that clock ran ahead of
// the database's put the row a fraction of a second in the FUTURE — so a
// purchase recorded a moment ago was classified as projected, and a user
// who recorded an expense and immediately asked what they had spent today
// was told nothing. Nothing errored; the number was simply wrong.
//
// This was found by this suite against a Postgres container running about
// four hundred milliseconds behind the host, which is an ordinary amount of
// skew and exactly what a separate database container in production is.
// The fix was to stop having two clocks — see ports.Clock.
func TestARecordedEventIsRealizedImmediately(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	out := e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 8990, "description": "mercado",
	})
	id := uuid.MustParse(str(t, out, "transaction_id"))

	sum := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "today"})
	realized := nested(t, sum, "realized")
	projected := nested(t, sum, "projected")

	if got := num(t, realized, "expense_cents"); got != 8990 {
		t.Fatalf("an expense recorded a moment ago is realized as %d cents, want 8990", got)
	}
	if got := num(t, projected, "expense_cents"); got != 0 {
		t.Fatalf("%d cents of a just-recorded expense landed in the FUTURE", got)
	}

	// And the stamp really is at or before the database's own clock — the
	// property, asserted against the same function the contract uses.
	var inPast bool
	if err := e.pool.QueryRow(context.Background(),
		`SELECT occurred_at <= now() FROM finance.transactions WHERE id = $1`, id).Scan(&inPast); err != nil {
		t.Fatalf("compare against the database clock: %v", err)
	}
	if !inPast {
		t.Fatal("the row was stamped in the database's future")
	}
}

/* ══ 10. parity: one record, read three ways ══════════════════════════ */

// The UI, the HTTP API and the capabilities must not be able to disagree
// about the same transaction.
//
// ── Why this is asserted at the application layer ──────────────────────
// Because that is where the three paths actually converge:
// `httpapi.Handler` (what the screens call) and `tools` (what Ledger calls)
// both hold the SAME `*app.Service` — see finance/module.go, which
// constructs one and hands the pointer to both. A test that mocked either
// side would be proving something about the mock. This drives the real
// service and reads the real rows back.
func TestOneRecordReadsTheSameThroughEveryPath(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	/* ── A. written through the screens' boundary, read by a tool ───── */
	// Dated YESTERDAY, not today. `e.today()` is midday local, which is in
	// the FUTURE whenever this suite runs in the morning — and a
	// future-dated paid row is PROJECTED, not REALIZED, by the totals
	// contract. That is the contract behaving correctly; it just makes
	// midday a bad stamp for a fixture that has to be realized.
	viaAPI, err := e.svc.CreateTransaction(ctxFor(e.wsA), app.CreateTransactionInput{
		WorkspaceID: e.wsA, CategoryID: cat.ID, AmountCents: 4550,
		Description: "fixture A", OccurredAt: e.daysAgo(1),
	})
	if err != nil {
		t.Fatalf("create through the application boundary: %v", err)
	}

	read := e.execute(t, e.wsA, TransactionGetTool, map[string]any{
		"transaction_id": viaAPI.ID.String(),
	})
	row := nested(t, read, "transaction")
	if got := num(t, row, "amount_cents"); got != 4550 {
		t.Fatalf("the capability reads %d cents for a row the screens wrote as 4550", got)
	}
	if got := str(t, row, "description"); got != "fixture A" {
		t.Fatalf("description differs: %q", got)
	}
	if got := str(t, row, "category"); got != "Mercado" {
		t.Fatalf("category differs: %q", got)
	}
	if got := str(t, row, "status"); got != string(viaAPI.Status) {
		t.Fatalf("status differs: %q vs %q", got, viaAPI.Status)
	}
	if got := str(t, row, "occurred_on"); got != viaAPI.OccurredAt.In(e.loc).Format("2006-01-02") {
		t.Fatalf("date differs: %q", got)
	}

	/* ── B. written by a capability, read through the screens' path ─── */
	created := e.execute(t, e.wsA, TransactionCreateTool, map[string]any{
		"category_id": cat.ID.String(), "amount_cents": 8990, "description": "fixture B",
	})
	id := uuid.MustParse(str(t, created, "transaction_id"))

	// The exact read the screens perform.
	back, err := e.svc.GetTransaction(ctxFor(e.wsA), e.wsA, id)
	if err != nil {
		t.Fatalf("read back through the application boundary: %v", err)
	}
	if back.AmountCents != 8990 {
		t.Fatalf("the screens read %d cents for a row a capability wrote as 8990", back.AmountCents)
	}
	// And in Postgres, bypassing every Go type between.
	if stored, _ := e.rawAmount(id); stored != 8990 {
		t.Fatalf("Postgres holds %d cents", stored)
	}
	// No duplicate was produced by writing through the other door.
	if n := e.liveCount(e.wsA); n != 2 {
		t.Fatalf("two writes produced %d rows", n)
	}

	/* ── C. an edit through one path is visible through the other ───── */
	if _, err := e.svc.UpdateTransaction(ctxFor(e.wsA), app.UpdateTransactionInput{
		WorkspaceID: e.wsA, ID: id, AmountCents: int64Ptr(7990),
	}); err != nil {
		t.Fatalf("update through the application boundary: %v", err)
	}
	reread := e.execute(t, e.wsA, TransactionGetTool, map[string]any{"transaction_id": id.String()})
	if got := num(t, nested(t, reread, "transaction"), "amount_cents"); got != 7990 {
		t.Fatalf("the capability still reads %d after the screens changed it to 7990", got)
	}

	/* ── D. totals parity, on canonical values ──────────────────────── */
	//
	// Compared as integers, never as rendered strings: the screens format
	// with Intl and the capability formats with its own helper, and two
	// different renderings of the same integer are not a disagreement.
	// The SAME window on both sides, or the comparison proves nothing. The
	// capability derives [from 00:00, to+1day 00:00) from calendar dates,
	// so the screens' call is given exactly those instants rather than an
	// approximation of them.
	fromDay := e.daysAgo(2)
	toDay := e.today()
	midnight := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, e.loc)
	}
	uiTotals, err := e.svc.GetTransactionTotals(ctxFor(e.wsA), app.GetTransactionTotalsInput{
		WorkspaceID: e.wsA, From: midnight(fromDay), To: midnight(toDay).AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("totals through the screens' boundary: %v", err)
	}
	summary := e.execute(t, e.wsA, SummaryGetTool, map[string]any{
		"from": fromDay.Format("2006-01-02"),
		"to":   toDay.Format("2006-01-02"),
	})
	realized := nested(t, summary, "realized")

	uiRealized := uiTotals.ByRealization[ports.RealizationRealized]
	if got := num(t, realized, "expense_cents"); got != uiRealized.ExpenseCents {
		t.Fatalf("expense disagrees: capability %d vs screens %d", got, uiRealized.ExpenseCents)
	}
	if got := num(t, realized, "income_cents"); got != uiRealized.IncomeCents {
		t.Fatalf("income disagrees: capability %d vs screens %d", got, uiRealized.IncomeCents)
	}
	if got := num(t, realized, "net_cents"); got != uiRealized.IncomeCents-uiRealized.ExpenseCents {
		t.Fatalf("net disagrees with the buckets it is derived from")
	}
	// The sum of the two fixtures, stated once so the parity above cannot
	// be satisfied by both sides being equally wrong.
	if uiRealized.ExpenseCents != 4550+7990 {
		t.Fatalf("the window holds %d cents, want %d", uiRealized.ExpenseCents, 4550+7990)
	}
}
