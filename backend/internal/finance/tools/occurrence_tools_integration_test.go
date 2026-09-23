//go:build integration

// The monthly-commitment capabilities, through the real runtime.
//
// The LLM is a script here, deliberately: what this file proves is which
// capability ran, what reached Postgres, who was refused, and what the
// audit trail kept. Whether a real model reads the month before writing,
// and whether it asks instead of guessing when two bills could be the one
// meant, are claims about judgement and live in ledger_live_test.go behind
// its own build tag.
//
// Every fixture is synthetic. No operator data is read or written.
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

// allOccurrenceTools is the monthly-commitment grant set, named once so a
// test that means "authorized for the month" cannot drift from the
// catalogue.
var allOccurrenceTools = []chatdomain.ToolName{
	RecurringMonthTool, MarkPaidTool, MarkPendingTool, SetMonthAmountTool,
}

// currentPeriod is the month the DATABASE is in, in the reporting zone.
// Never this process's clock: the tools are cut from the database's, and a
// test with its own would disagree with them on the last evening of a
// month.
func (e *env) currentPeriod(t *testing.T) domain.Period {
	t.Helper()
	now, err := e.svc.Now(context.Background())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	return domain.PeriodOf(now, e.loc)
}

// seedRecurring creates a definition that has been running for a year, so
// any month this suite asks about is one it was applicable in.
func (e *env) seedRecurring(
	ws uuid.UUID, cat *domain.Category, desc string, cents int64, dueDay int, varies bool,
) *domain.RecurringEntry {
	e.t.Helper()
	f, err := e.svc.CreateRecurringEntry(ctxFor(ws), app.CreateRecurringEntryInput{
		WorkspaceID: ws, Description: desc, AmountCents: cents,
		CategoryID: cat.ID, DueDay: dueDay, AmountVaries: varies,
	})
	if err != nil {
		e.t.Fatalf("seed recurring %s: %v", desc, err)
	}
	// Backdated through SQL rather than the service, because `starts_at`
	// is stamped from the database clock on create and is not an input the
	// application layer offers: making it one so a test could move it
	// would be widening a write path for the convenience of a fixture.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE finance.recurring_entries SET starts_at = now() - interval '1 year' WHERE id = $1`,
		f.ID); err != nil {
		e.t.Fatalf("backdate %s: %v", desc, err)
	}
	reloaded, err := e.svc.GetRecurringEntry(ctxFor(ws), ws, f.ID)
	if err != nil {
		e.t.Fatalf("reload %s: %v", desc, err)
	}
	return reloaded
}

/* ── the read ────────────────────────────────────────────────────────── */

func TestMonthCapabilityReportsTheMonth(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	power := e.seedRecurring(e.wsA, cat, "luz", 42000, 25, true)

	out := e.execute(t, e.wsA, RecurringMonthTool, nil)

	// The authoritative date, always, so a model that narrates the month
	// describes the right one.
	if str(t, out, "today") == "" || str(t, out, "time_zone") == "" {
		t.Fatalf("the month carries no authoritative date: %v", out)
	}
	if got := str(t, out, "period"); got != e.currentPeriod(t).String() {
		t.Fatalf("period is %q, want the current month", got)
	}
	if proj, _ := out["is_projection"].(bool); proj {
		t.Fatal("the current month came back as a projection")
	}

	// Server-computed totals, both ways for every amount.
	if got := num(t, out, "committed_cents"); got != 292000 {
		t.Fatalf("committed_cents is %d, want 292000", got)
	}
	if got := num(t, out, "remaining_cents"); got != 292000 {
		t.Fatalf("remaining_cents is %d", got)
	}
	if got := num(t, out, "estimated_cents"); got != 42000 {
		t.Fatalf("estimated_cents is %d, want the varying bill's 42000", got)
	}
	// Rendered alongside the integer, from the same value: 292000 CENTS is
	// R$ 2.920,00. A reader who confuses the two is exactly who the pair
	// exists for.
	if got := str(t, out, "committed"); got != "R$ 2.920,00" {
		t.Fatalf("the rendered total is %q", got)
	}
	for _, k := range []string{"occurrence_count", "paid_count", "pending_count",
		"estimated_count", "overdue_count"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("the result has no %s", k)
		}
	}

	rows := rows(t, out, "occurrences")
	if len(rows) != 2 {
		t.Fatalf("the month lists %d obligations", len(rows))
	}
	byEntry := map[string]map[string]any{}
	for _, r := range rows {
		byEntry[str(t, r, "recurring_entry_id")] = r
	}
	rentRow, ok := byEntry[rent.ID.String()]
	if !ok {
		t.Fatal("the rent is not in the month")
	}
	// Enough identity for a write to target it, without a second opaque id.
	if str(t, rentRow, "period") != str(t, out, "period") {
		t.Fatal("the row does not carry the month it belongs to")
	}
	if str(t, rentRow, "description") != "aluguel" || str(t, rentRow, "category") != "Casa" {
		t.Fatalf("the row carries no recognition: %v", rentRow)
	}
	if str(t, rentRow, "status") != "pending" || str(t, rentRow, "due_on") == "" {
		t.Fatalf("the row reads %v", rentRow)
	}
	if est, _ := rentRow["amount_estimated"].(bool); est {
		t.Fatal("a fixed obligation in the current month is marked an estimate")
	}
	if est, _ := byEntry[power.ID.String()]["amount_estimated"].(bool); !est {
		t.Fatal("a varying obligation is not marked an estimate")
	}
}

func TestMonthCapabilityProjectsAFutureMonth(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	future := e.currentPeriod(t).Next()

	out := e.execute(t, e.wsA, RecurringMonthTool, map[string]any{"period": future.String()})
	if proj, _ := out["is_projection"].(bool); !proj {
		t.Fatal("a month that has not begun did not declare itself a projection")
	}
	// The note is the part a model actually reads before narrating.
	if note := str(t, out, "note"); !strings.Contains(note, "not begun") {
		t.Fatalf("the projection does not say what it is: %q", note)
	}
	var rowCount int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.recurring_occurrences`).Scan(&rowCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("reading a future month through the capability wrote %d rows", rowCount)
	}
}

// A definition nothing can place is reported, and the month still answers.
func TestMonthCapabilityReportsUnplaceableEntries(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)

	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO finance.recurring_entries
		   (id, workspace_id, description, amount_cents, category_id, due_day,
		    recurrence, status, starts_at)
		 VALUES (gen_random_uuid(),$1,'seguro anual',120000,$2,10,'annual','active',
		         now() - interval '1 year')`,
		e.wsA, cat.ID); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	out := e.execute(t, e.wsA, RecurringMonthTool, nil)
	if got := num(t, out, "committed_cents"); got != 250000 {
		t.Fatalf("the legacy annual row was counted somewhere: committed %d", got)
	}
	missing := rows(t, out, "unplaceable")
	if len(missing) != 1 || str(t, missing[0], "reason") != string(app.MissingDueMonth) {
		t.Fatalf("unplaceable reads %v", missing)
	}
	// The totals must be declared incomplete, in the result, not only in a
	// comment: a silently short total is the failure this module is
	// arranged against.
	if note := str(t, out, "unplaceable_note"); !strings.Contains(note, "incomplete") {
		t.Fatalf("the incompleteness is not stated: %q", note)
	}
}

func TestMonthCapabilityRejectsAMalformedPeriod(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{"2026-9", "2026-13", "2026-09-01", "setembro"} {
		if _, err := e.executeErr(e.wsA, RecurringMonthTool, map[string]any{"period": bad}); err == nil {
			t.Fatalf("period=%q was accepted", bad)
		}
	}
}

/* ── the writes ──────────────────────────────────────────────────────── */

func TestMarkPaidCapabilitySettlesExactlyOneMonth(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	other := e.seedRecurring(e.wsA, cat, "internet", 13000, 15, false)
	e.execute(t, e.wsA, RecurringMonthTool, nil) // materialise

	out := e.execute(t, e.wsA, MarkPaidTool, map[string]any{
		"recurring_entry_id": rent.ID.String(),
	})
	if str(t, out, "status") != "paid" || str(t, out, "paid_on") == "" {
		t.Fatalf("mark_paid returned %v", out)
	}
	if paid, _ := out["marked_paid"].(bool); !paid {
		t.Fatal("mark_paid did not declare itself")
	}
	// The result says, in the result rather than only in the description,
	// that no money was recorded as having moved.
	if note := str(t, out, "note"); !strings.Contains(note, "No transaction") {
		t.Fatalf("the result does not say a transaction was not created: %q", note)
	}

	month := e.execute(t, e.wsA, RecurringMonthTool, nil)
	if got := num(t, month, "paid_cents"); got != 250000 {
		t.Fatalf("the month says %d paid", got)
	}
	if got := num(t, month, "paid_count"); got != 1 {
		t.Fatalf("the month says %d bills paid", got)
	}
	// The other obligation is untouched: one call, one row.
	for _, r := range rows(t, month, "occurrences") {
		if str(t, r, "recurring_entry_id") == other.ID.String() && str(t, r, "status") != "pending" {
			t.Fatal("marking one bill paid settled another")
		}
	}

	// NOTHING reached the ledger. This is the load-bearing negative.
	var txCount int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.transactions WHERE workspace_id = $1`, e.wsA).Scan(&txCount); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if txCount != 0 {
		t.Fatalf("marking a bill paid created %d transactions", txCount)
	}
}

// Re-issuing mark_paid, which is exactly what a retried tool call looks
// like, must not undo the first one.
func TestMarkPaidCapabilityDoesNotToggle(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	args := map[string]any{"recurring_entry_id": rent.ID.String()}
	first := e.execute(t, e.wsA, MarkPaidTool, args)
	stamp := str(t, first, "paid_on")

	for i := 0; i < 3; i++ {
		again := e.execute(t, e.wsA, MarkPaidTool, args)
		if str(t, again, "status") != "paid" {
			t.Fatalf("repeat %d toggled the bill to %q", i+1, str(t, again, "status"))
		}
		if str(t, again, "paid_on") != stamp {
			t.Fatalf("repeat %d moved the payment date", i+1)
		}
	}
	if got := num(t, e.execute(t, e.wsA, RecurringMonthTool, nil), "paid_count"); got != 1 {
		t.Fatalf("after four marks the month says %d bills paid", got)
	}
}

func TestMarkPendingCapabilityUndoesAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	args := map[string]any{"recurring_entry_id": rent.ID.String()}
	e.execute(t, e.wsA, MarkPaidTool, args)

	out := e.execute(t, e.wsA, MarkPendingTool, args)
	if str(t, out, "status") != "pending" {
		t.Fatalf("mark_pending returned %v", out)
	}
	if _, present := out["paid_on"]; present {
		t.Fatal("an unsettled month still reports a payment date")
	}
	if got := num(t, out, "amount_cents"); got != 250000 {
		t.Fatalf("mark_pending changed the amount to %d", got)
	}

	for i := 0; i < 3; i++ {
		again := e.execute(t, e.wsA, MarkPendingTool, args)
		if str(t, again, "status") != "pending" {
			t.Fatalf("repeat %d toggled it to %q", i+1, str(t, again, "status"))
		}
	}
	if got := num(t, e.execute(t, e.wsA, RecurringMonthTool, nil), "remaining_cents"); got != 250000 {
		t.Fatalf("after unsettling, the month says %d remaining", got)
	}
}

func TestSetMonthAmountCapability(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	power := e.seedRecurring(e.wsA, cat, "luz", 42000, 25, true)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	args := map[string]any{"recurring_entry_id": power.ID.String(), "amount_cents": 43720}
	out := e.execute(t, e.wsA, SetMonthAmountTool, args)
	if got := num(t, out, "amount_cents"); got != 43720 {
		t.Fatalf("set_month_amount stored %d cents", got)
	}
	if est, _ := out["amount_estimated"].(bool); est {
		t.Fatal("a confirmed amount is still marked an estimate")
	}
	// It does NOT settle the bill. Two facts, two capabilities.
	if str(t, out, "status") != "pending" {
		t.Fatalf("setting the amount also marked it %q", str(t, out, "status"))
	}
	if note := str(t, out, "note"); !strings.Contains(note, "NOT marked paid") {
		t.Fatalf("the result does not say the bill was left pending: %q", note)
	}

	// The DEFINITION's default is untouched.
	reloaded, err := e.svc.GetRecurringEntry(ctxFor(e.wsA), e.wsA, power.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AmountCents != 42000 {
		t.Fatalf("the recurrence's default became %d", reloaded.AmountCents)
	}

	// A decimal is refused by the schema, before anything runs — the same
	// guard finance.transaction.create has.
	if _, err := e.executeRaw(e.wsA, SetMonthAmountTool,
		`{"recurring_entry_id":"`+power.ID.String()+`","amount_cents":437.20}`); err == nil {
		t.Fatal("a decimal amount was accepted")
	}

	// A settled month is refused rather than rewritten.
	e.execute(t, e.wsA, MarkPaidTool, map[string]any{"recurring_entry_id": power.ID.String()})
	if _, err := e.executeErr(e.wsA, SetMonthAmountTool, args); err == nil {
		t.Fatal("a paid month had its amount rewritten")
	}
}

/* ── authorization, isolation, and the audit trail ───────────────────── */

// A grant is what authorizes, and the name of the agent is not.
func TestOccurrenceCapabilitiesRequireGrants(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	// An agent called Ledger holding NO monthly-commitment grant.
	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, CategoryListTool)

	e.llm.scriptToolCall(RecurringMonthTool, `{}`)
	sink := e.turn(e.wsA, convID, "o que falta pagar esse mês?")
	if ev, ok := sink.finished(RecurringMonthTool); ok && ev.Status == "ok" {
		t.Fatal("an unauthorized month read succeeded")
	}

	e.llm.scriptToolCall(MarkPaidTool, `{"recurring_entry_id":"`+rent.ID.String()+`"}`)
	sink = e.turn(e.wsA, convID, "paguei o aluguel")
	if ev, ok := sink.finished(MarkPaidTool); ok && ev.Status == "ok" {
		t.Fatal("an unauthorized mark_paid succeeded")
	}
	month := e.execute(t, e.wsA, RecurringMonthTool, nil)
	if got := num(t, month, "paid_cents"); got != 0 {
		t.Fatalf("an unauthorized agent settled %d cents", got)
	}

	// With the grants, the same calls go through.
	e.authorize(e.wsA, agentID, allOccurrenceTools...)
	e.llm.scriptToolCall(MarkPaidTool, `{"recurring_entry_id":"`+rent.ID.String()+`"}`)
	sink = e.turn(e.wsA, convID, "paguei o aluguel")
	if ev, ok := sink.finished(MarkPaidTool); !ok || ev.Status != "ok" {
		t.Fatalf("an authorized mark_paid did not run: %+v", ev)
	}
}

func TestOccurrenceCapabilitiesAreWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	// B reads its own month and finds nothing of A's.
	if got := num(t, e.execute(t, e.wsB, RecurringMonthTool, nil), "committed_cents"); got != 0 {
		t.Fatalf("workspace B sees %d cents of workspace A's obligations", got)
	}
	// And B cannot settle A's bill by naming it exactly.
	for _, name := range []chatdomain.ToolName{MarkPaidTool, MarkPendingTool} {
		if _, err := e.executeErr(e.wsB, name, map[string]any{
			"recurring_entry_id": rent.ID.String(),
		}); err == nil {
			t.Fatalf("workspace B wrote through %s", name)
		}
	}
	if _, err := e.executeErr(e.wsB, SetMonthAmountTool, map[string]any{
		"recurring_entry_id": rent.ID.String(), "amount_cents": 1,
	}); err == nil {
		t.Fatal("workspace B set the amount of workspace A's obligation")
	}
	if got := num(t, e.execute(t, e.wsA, RecurringMonthTool, nil), "paid_cents"); got != 0 {
		t.Fatal("workspace A's month was written by B")
	}
}

// No capability takes a workspace, and none may ever.
//
// The workspace comes from the context. An argument for it would be a
// field through which a conversation could propose somebody else's money,
// and a prompt that mentioned one would eventually be believed.
func TestOccurrenceCapabilitiesHaveNoWorkspaceArgument(t *testing.T) {
	e := newEnv(t)
	for _, name := range allOccurrenceTools {
		tool, ok := e.registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		def := tool.Definition()
		for prop := range def.Schema.Properties {
			if strings.Contains(strings.ToLower(prop), "workspace") {
				t.Errorf("%s offers a %q argument", name, prop)
			}
		}
		// Confidential, and never External: these describe OUR Postgres.
		if !def.Confidential {
			t.Errorf("%s is not Confidential", name)
		}
		if def.External {
			t.Errorf("%s is marked External; it reads this product's own database", name)
		}
	}
}

// The three verbs declare themselves as writes, and the read as a read.
// The receipt a turn produces is derived from this, so an effect that was
// wrong here would make the product's evidence wrong.
func TestOccurrenceCapabilityEffectsAreDeclared(t *testing.T) {
	e := newEnv(t)
	want := map[chatdomain.ToolName]chatdomain.ToolEffect{
		RecurringMonthTool: chatdomain.EffectRead,
		MarkPaidTool:       chatdomain.EffectWrite,
		MarkPendingTool:    chatdomain.EffectWrite,
		SetMonthAmountTool: chatdomain.EffectWrite,
	}
	for name, effect := range want {
		tool, ok := e.registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if got := tool.Definition().Effect; got != effect {
			t.Errorf("%s declares effect %q, want %q", name, got, effect)
		}
	}
}

// A settlement leaves a trail that says WHAT ran and keeps none of what it
// carried.
func TestOccurrencePayloadsAreRedactedInTheAuditTrail(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	power := e.seedRecurring(e.wsA, cat, "luz", 42000, 25, true)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allOccurrenceTools...)

	e.llm.scriptToolCall(SetMonthAmountTool,
		`{"recurring_entry_id":"`+power.ID.String()+`","amount_cents":43720}`)
	sink := e.turn(e.wsA, convID, "a luz veio 437,20")
	if ev, _ := sink.finished(SetMonthAmountTool); ev.Status != "ok" {
		t.Fatalf("the call did not succeed: %s", ev.ErrorCode)
	}

	var name, status string
	var args, result *string
	var redacted bool
	var effect string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT tool_name, status, arguments, result, redacted, effect
		   FROM chat.tool_calls WHERE conversation_id = $1`, convID).
		Scan(&name, &status, &args, &result, &redacted, &effect); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if name != string(SetMonthAmountTool) || status != "ok" || effect != "write" {
		t.Fatalf("the trail reads %s/%s effect=%s", name, status, effect)
	}
	if !redacted || args != nil || result != nil {
		t.Fatalf("the payload was kept: redacted=%v args=%v result=%v", redacted, args, result)
	}

	var whole string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT coalesce(arguments,'') || ' ' || coalesce(result,'') || ' ' || coalesce(error_message,'')
		   FROM chat.tool_calls WHERE conversation_id = $1`, convID).Scan(&whole); err != nil {
		t.Fatalf("read audit text: %v", err)
	}
	for _, secret := range []string{"43720", "437,20", "luz"} {
		if strings.Contains(whole, secret) {
			t.Errorf("the audit row still contains %q", secret)
		}
	}
}

// A turn that settled a bill produces a receipt that says so, derived from
// execution and not from anything the model wrote.
func TestSettlingProducesAWriteReceipt(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	rent := e.seedRecurring(e.wsA, cat, "aluguel", 250000, 5, false)
	e.execute(t, e.wsA, RecurringMonthTool, nil)

	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allOccurrenceTools...)

	e.llm.scriptToolCall(MarkPaidTool, `{"recurring_entry_id":"`+rent.ID.String()+`"}`)
	sink := e.turn(e.wsA, convID, "paguei o aluguel")
	if ev, _ := sink.finished(MarkPaidTool); ev.Status != "ok" {
		t.Fatalf("the call did not succeed: %s", ev.ErrorCode)
	}

	messages, err := e.chatSvc.ListMessages(ctxFor(e.wsA), e.wsA, convID, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	last := messages[len(messages)-1]
	receipts, err := e.chatSvc.WriteReceipts(ctxFor(e.wsA), e.wsA, []uuid.UUID{last.ID})
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	r := receipts[last.ID]
	if !r.Confirmed() || r.Executed != 1 || r.Failed != 0 || r.Refused != 0 {
		t.Fatalf("the receipt reads executed=%d failed=%d refused=%d",
			r.Executed, r.Failed, r.Refused)
	}
	if len(r.Writes) != 1 || r.Writes[0].Capability != MarkPaidTool {
		t.Fatalf("the receipt names %v", r.Writes)
	}

	// A READ produces no write receipt: a listing that ran is not a change.
	e.llm.scriptToolCall(RecurringMonthTool, `{}`)
	sink = e.turn(e.wsA, convID, "e o que falta?")
	if ev, _ := sink.finished(RecurringMonthTool); ev.Status != "ok" {
		t.Fatalf("the read did not succeed: %s", ev.ErrorCode)
	}
	messages, err = e.chatSvc.ListMessages(ctxFor(e.wsA), e.wsA, convID, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	last = messages[len(messages)-1]
	receipts, err = e.chatSvc.WriteReceipts(ctxFor(e.wsA), e.wsA, []uuid.UUID{last.ID})
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	if got := receipts[last.ID]; got.Confirmed() || got.Executed != 0 {
		t.Fatalf("a read produced a write receipt: %+v", got)
	}
}

// A month with many obligations still fits in one tool call, and says so
// if it had to be cut.
func TestMonthCapabilityRespectsTheResultCeiling(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Casa", domain.EntryTypeExpense)
	for i := 0; i < 120; i++ {
		e.seedRecurring(e.wsA, cat, "fixture obligation", int64(1000+i), (i%28)+1, false)
	}
	out := e.execute(t, e.wsA, RecurringMonthTool, nil)

	if size(out) > maxToolResultBytes {
		t.Fatalf("the result is %d bytes, above the %d ceiling", size(out), maxToolResultBytes)
	}
	// Truncated or not, the TOTALS are the server's and stay correct: they
	// describe the whole month even when the listing had to be cut.
	if got := num(t, out, "occurrence_count"); got != 120 {
		t.Fatalf("the month counts %d obligations, want 120", got)
	}
	if truncated, _ := out["truncated"].(bool); truncated {
		if note := str(t, out, "truncated_note"); note == "" {
			t.Fatal("a truncated result did not declare itself")
		}
	}
}
