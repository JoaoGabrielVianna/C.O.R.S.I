//go:build integration

package tools

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

// The capabilities added when Ledger stopped being a transactions-only
// agent: the vocabulary (categories, cards, people) and the commitments.

/* ══ the catalogue ════════════════════════════════════════════════════ */

var allDomainTools = []chatdomain.ToolName{
	CategoryCreateTool, CategoryUpdateTool,
	CardListTool, CardUpdateTool,
	PersonListTool, PersonCreateTool, PersonUpdateTool,
	RecurringListTool, RecurringCreateTool, RecurringUpdateTool,
	RecurringSummaryTool,
	RecurringMonthTool, MarkPaidTool, MarkPendingTool, SetMonthAmountTool,
}

// The capability names, asserted literally.
//
// A rename revokes every grant that referenced the old name, so the names
// are part of the contract in a way a Go identifier is not. Spelling them
// out here means a rename cannot happen by accident.
func TestCapabilityNamesAreTheContract(t *testing.T) {
	e := newEnv(t)
	want := []string{
		"finance.card.list", "finance.card.update",
		"finance.category.create", "finance.category.list", "finance.category.update",
		"finance.import.commit", "finance.import.prepare", "finance.import.resolve_group",
		"finance.import_source.create", "finance.import_source.list",
		"finance.person.create", "finance.person.list", "finance.person.update",
		"finance.recurring_entry.create", "finance.recurring_entry.list",
		// The monthly-commitment four: one read of a month, and the three
		// verbs that write one. No occurrence.update and no toggle — see
		// occurrence_tools.go.
		"finance.recurring_entry.mark_paid", "finance.recurring_entry.mark_pending",
		"finance.recurring_entry.month",
		"finance.recurring_entry.set_month_amount",
		"finance.recurring_entry.summary", "finance.recurring_entry.update",
		"finance.summary.get",
		"finance.transaction.create", "finance.transaction.delete",
		"finance.transaction.get", "finance.transaction.list", "finance.transaction.update",
	}
	got := []string{}
	for _, d := range e.registry.Definitions() {
		if strings.HasPrefix(string(d.Name), "finance.") {
			got = append(got, string(d.Name))
		}
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("finance offers %d capabilities, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("capability %d is %q, want %q", i, got[i], want[i])
		}
	}
	// The old names must be gone: no aliases, because no published release
	// depends on them.
	for _, gone := range []chatdomain.ToolName{
		"finance.fixed_expense.list", "finance.fixed_expense.create",
		"finance.fixed_expense.update", "finance.commitment.get",
	} {
		if _, ok := e.registry.Lookup(gone); ok {
			t.Errorf("%s still exists; it was renamed, not aliased", gone)
		}
	}
}

func TestDomainCapabilitiesAreRegisteredAndConfidential(t *testing.T) {
	e := newEnv(t)
	for _, name := range append(append([]chatdomain.ToolName{}, allFinanceTools...), allDomainTools...) {
		tool, ok := e.registry.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		def := tool.Definition()
		if err := def.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Every Finance capability carries money. None of them may keep its
		// payload in the audit trail in the clear.
		if !def.Confidential {
			t.Errorf("%s does not declare itself confidential", name)
		}
	}
}

// Nothing dangerous was added by symmetry.
func TestNoDeleteCapabilityForSharedVocabulary(t *testing.T) {
	e := newEnv(t)
	// Removing a category or a person that transactions reference would
	// leave that history pointing at a label nothing resolves — the
	// application's soft delete has no in-use guard. The response is to not
	// offer the operation, and this is what says so.
	for _, absent := range []chatdomain.ToolName{
		"finance.category.delete",
		"finance.person.delete",
		"finance.card.delete",
		"finance.card.archive",
		"finance.card.create",
		"finance.recurring_entry.delete",
	} {
		if _, ok := e.registry.Lookup(absent); ok {
			t.Errorf("%s exists; it was deliberately not offered", absent)
		}
	}
}

/* ══ category ═════════════════════════════════════════════════════════ */

func TestCategoryCreateAndRename(t *testing.T) {
	e := newEnv(t)

	out := e.execute(t, e.wsA, CategoryCreateTool, map[string]any{
		"name": "Viagens", "type": "expense",
	})
	id := uuid.MustParse(str(t, out, "category_id"))
	if str(t, out, "type") != "expense" {
		t.Fatalf("type is %q", str(t, out, "type"))
	}

	// Visible through the screens' own read path.
	cats, err := e.svc.ListCategories(ctxFor(e.wsA), app.ListCategoriesInput{WorkspaceID: e.wsA, Limit: 50})
	if err != nil || len(cats) != 1 || cats[0].Name != "Viagens" {
		t.Fatalf("the screens read %v (err %v)", cats, err)
	}

	renamed := e.execute(t, e.wsA, CategoryUpdateTool, map[string]any{
		"category_id": id.String(), "name": "Viagem",
	})
	if str(t, renamed, "name") != "Viagem" || str(t, renamed, "previous_name") != "Viagens" {
		t.Fatalf("rename reported %v", renamed)
	}

	// A transaction filed under it keeps its place across the rename.
	tx, err := e.svc.CreateTransaction(ctxFor(e.wsA), app.CreateTransactionInput{
		WorkspaceID: e.wsA, CategoryID: id, AmountCents: 5000,
		Description: "passagem", OccurredAt: e.daysAgo(1),
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	read := e.execute(t, e.wsA, TransactionGetTool, map[string]any{"transaction_id": tx.ID.String()})
	if got := str(t, nested(t, read, "transaction"), "category"); got != "Viagem" {
		t.Fatalf("the transaction reads category %q after the rename", got)
	}
}

/* ══ card ═════════════════════════════════════════════════════════════ */

func (e *env) seedCard(ws uuid.UUID, name, institution, last4 string, limit int64) *domain.Card {
	e.t.Helper()
	c, err := e.svc.CreateCard(ctxFor(ws), app.CreateCardInput{
		WorkspaceID: ws, Name: name, Institution: institution,
		Network: domain.CardNetworkVisa, Last4: last4,
		LimitCents: limit, ClosingDay: 22, DueDay: 1,
	})
	if err != nil {
		e.t.Fatalf("seed card %s: %v", name, err)
	}
	return c
}

func TestCardListReportsWhatIsStoredAndSaysWhatIsNot(t *testing.T) {
	e := newEnv(t)
	e.seedCard(e.wsA, "Nubank Black", "Nubank", "1234", 1_500_000)

	out := e.execute(t, e.wsA, CardListTool, nil)
	rows := rows(t, out, "cards")
	if len(rows) != 1 {
		t.Fatalf("listed %d cards", len(rows))
	}
	card := rows[0]
	if num(t, card, "limit_cents") != 1_500_000 {
		t.Fatalf("limit is %d cents", num(t, card, "limit_cents"))
	}
	if str(t, card, "limit") != "R$ 15.000,00" {
		t.Fatalf("limit renders as %q", str(t, card, "limit"))
	}
	for _, f := range []string{"closing_day", "due_day", "network", "last4", "institution"} {
		if _, ok := card[f]; !ok {
			t.Errorf("card row is missing %s, which Finance does store", f)
		}
	}
	// Finance stores no balance and no available limit. The result says so
	// rather than leaving a model to compute one from spending.
	if _, ok := out["not_recorded"]; !ok {
		t.Fatal("the result does not state what Finance does NOT hold about a card")
	}
	for _, invented := range []string{"balance", "available", "available_limit", "current_bill"} {
		if _, ok := card[invented]; ok {
			t.Errorf("card row invented %q, which the domain has no field for", invented)
		}
	}
}

func TestCardUpdateChangesLimitAndReportsThePrevious(t *testing.T) {
	e := newEnv(t)
	card := e.seedCard(e.wsA, "Nubank Black", "Nubank", "1234", 1_500_000)

	out := e.execute(t, e.wsA, CardUpdateTool, map[string]any{
		"card_id": card.ID.String(), "limit_cents": 1_800_000,
	})
	if num(t, out, "limit_cents") != 1_800_000 {
		t.Fatalf("limit is now %d", num(t, out, "limit_cents"))
	}
	if num(t, out, "previous_limit_cents") != 1_500_000 {
		t.Fatalf("previous limit reported as %d", num(t, out, "previous_limit_cents"))
	}
	// In Postgres, as cents.
	var stored int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT limit_cents FROM finance.cards WHERE id = $1`, card.ID).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored != 1_800_000 {
		t.Fatalf("Postgres holds %d", stored)
	}

	// A call that changes nothing is refused rather than reported as a
	// successful no-op.
	if _, err := e.executeErr(e.wsA, CardUpdateTool, map[string]any{"card_id": card.ID.String()}); err == nil {
		t.Fatal("an update with no fields was accepted")
	}
}

// Two cards from the same bank: the capability offers no way to pick one by
// name, so a model must resolve the id first and cannot guess.
func TestCardUpdateCannotGuessBetweenTwoCandidates(t *testing.T) {
	e := newEnv(t)
	a := e.seedCard(e.wsA, "Nubank Black", "Nubank", "1234", 1_000_000)
	b := e.seedCard(e.wsA, "Nubank Ultravioleta", "Nubank", "5678", 2_000_000)

	// The listing shows both, told apart by name and last four digits.
	out := e.execute(t, e.wsA, CardListTool, map[string]any{"search": "nubank"})
	if got := len(rows(t, out, "cards")); got != 2 {
		t.Fatalf("search matched %d cards, want both candidates", got)
	}

	// The update schema takes an id and nothing else that could name a card,
	// so "the Nubank one" is not expressible as an argument.
	tool, _ := e.registry.Lookup(CardUpdateTool)
	for _, prop := range []string{"name_match", "institution", "search", "card_name"} {
		if _, ok := tool.Definition().Schema.Properties[prop]; ok {
			t.Errorf("card update accepts %q, which would let a model pick a card by guessing", prop)
		}
	}
	// Neither card moved by listing them.
	for _, c := range []*domain.Card{a, b} {
		got, err := e.svc.GetCard(ctxFor(e.wsA), e.wsA, c.ID)
		if err != nil || got.LimitCents != c.LimitCents {
			t.Fatalf("card %s changed while being read", c.Name)
		}
	}
}

/* ══ person ═══════════════════════════════════════════════════════════ */

func TestPersonCreateListRename(t *testing.T) {
	e := newEnv(t)

	created := e.execute(t, e.wsA, PersonCreateTool, map[string]any{"name": "Maria"})
	id := uuid.MustParse(str(t, created, "person_id"))

	listed := e.execute(t, e.wsA, PersonListTool, nil)
	if num(t, listed, "count") != 1 {
		t.Fatalf("count is %d", num(t, listed, "count"))
	}

	renamed := e.execute(t, e.wsA, PersonUpdateTool, map[string]any{
		"person_id": id.String(), "name": "Maria Silva",
	})
	if str(t, renamed, "name") != "Maria Silva" || str(t, renamed, "previous_name") != "Maria" {
		t.Fatalf("rename reported %v", renamed)
	}
	// Same row, through the screens' path.
	p, err := e.svc.GetPerson(ctxFor(e.wsA), e.wsA, id)
	if err != nil || p.Name != "Maria Silva" {
		t.Fatalf("the screens read %v (err %v)", p, err)
	}
	if _, err := e.executeErr(e.wsA, PersonUpdateTool, map[string]any{"person_id": id.String()}); err == nil {
		t.Fatal("an update with no fields was accepted")
	}
}

/* ══ fixed expenses ═══════════════════════════════════════════════════ */

// The rule the whole entity exists to hold.
func TestRecordingARecurringEntryCreatesNoTransaction(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Moradia", domain.EntryTypeExpense)

	before := e.liveCount(e.wsA)
	out := e.execute(t, e.wsA, RecurringCreateTool, map[string]any{
		"description": "Aluguel", "amount_cents": 180_000,
		"category_id": cat.ID.String(), "due_day": 5,
	})
	if num(t, out, "amount_cents") != 180_000 {
		t.Fatalf("amount is %d", num(t, out, "amount_cents"))
	}
	if str(t, out, "amount") != "R$ 1.800,00" {
		t.Fatalf("amount renders as %q", str(t, out, "amount"))
	}
	// The ledger did not move.
	if after := e.liveCount(e.wsA); after != before {
		t.Fatalf("recording a commitment created %d transactions", after-before)
	}
	var txRows int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.transactions WHERE workspace_id = $1`, e.wsA).Scan(&txRows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if txRows != 0 {
		t.Fatalf("finance.transactions holds %d rows after recording a commitment", txRows)
	}
	// And it IS in its own table, in cents.
	id := uuid.MustParse(str(t, out, "recurring_entry_id"))
	var cents int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT amount_cents FROM finance.recurring_entries WHERE id = $1`, id).Scan(&cents); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cents != 180_000 {
		t.Fatalf("Postgres holds %d cents", cents)
	}
}

// The whole point of the generalisation: a salary is a recurring entry.
func TestRecurringEntryAcceptsBothDirections(t *testing.T) {
	e := newEnv(t)
	income := e.seedCategory(e.wsA, "Salário", domain.EntryTypeIncome)
	expense := e.seedCategory(e.wsA, "Moradia", domain.EntryTypeExpense)

	salary := e.execute(t, e.wsA, RecurringCreateTool, map[string]any{
		"description": "Salário", "amount_cents": 800_000,
		"category_id": income.ID.String(), "due_day": 5,
	})
	rent := e.execute(t, e.wsA, RecurringCreateTool, map[string]any{
		"description": "Aluguel", "amount_cents": 180_000,
		"category_id": expense.ID.String(), "due_day": 10,
	})
	if str(t, salary, "description") != "Salário" || str(t, rent, "description") != "Aluguel" {
		t.Fatalf("create reported %v / %v", salary, rent)
	}

	// The listing reports each one's direction, read through its category —
	// there is no direction column on the entry.
	listed := e.execute(t, e.wsA, RecurringListTool, nil)
	dirs := map[string]string{}
	for _, row := range rows(t, listed, "recurring_entries") {
		dirs[str(t, row, "description")] = str(t, row, "direction")
	}
	if dirs["Salário"] != "income" || dirs["Aluguel"] != "expense" {
		t.Fatalf("directions read as %v", dirs)
	}

	// And the summary keeps them apart rather than netting them into one
	// figure that hides which way the money goes.
	sum := e.execute(t, e.wsA, RecurringSummaryTool, nil)
	if got := num(t, sum, "income_monthly_cents"); got != 800_000 {
		t.Fatalf("income total is %d", got)
	}
	if got := num(t, sum, "expense_monthly_cents"); got != 180_000 {
		t.Fatalf("expense total is %d", got)
	}
	if got := num(t, sum, "net_monthly_cents"); got != 800_000-180_000 {
		t.Fatalf("net is %d", got)
	}
	// Still no transactions: a recurrence is not money that moved, in
	// either direction.
	if e.liveCount(e.wsA) != 0 {
		t.Fatalf("recording recurrences created %d transactions", e.liveCount(e.wsA))
	}
}

// A category that does not exist in this workspace is refused — the guard
// that replaced the old expense-only rule.
func TestRecurringEntryRefusesAnUnknownCategory(t *testing.T) {
	e := newEnv(t)
	if _, err := e.executeErr(e.wsA, RecurringCreateTool, map[string]any{
		"description": "algo", "amount_cents": 1000,
		"category_id": uuid.New().String(), "due_day": 5,
	}); err == nil {
		t.Fatal("a recurring entry was created against a category that does not exist")
	}
	// Another workspace's category is equally refused.
	other := e.seedCategory(e.wsB, "Moradia", domain.EntryTypeExpense)
	if _, err := e.executeErr(e.wsA, RecurringCreateTool, map[string]any{
		"description": "algo", "amount_cents": 1000,
		"category_id": other.ID.String(), "due_day": 5,
	}); err == nil {
		t.Fatal("a recurring entry was created against another workspace's category")
	}
}

func TestRecurringSummaryNormalisesAnnualAndExcludesCancelled(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Assinaturas", domain.EntryTypeExpense)
	// dueMonth is sent only for an annual entry, because that is the rule:
	// an annual recurrence has to say WHICH month it falls in, and a
	// monthly one is refused the field. See RecurringEntry.Validate.
	mk := func(desc string, cents int64, rec string, dueMonth int) uuid.UUID {
		args := map[string]any{
			"description": desc, "amount_cents": cents,
			"category_id": cat.ID.String(), "due_day": 10,
		}
		if rec != "" {
			args["recurrence"] = rec
		}
		if dueMonth != 0 {
			args["due_month"] = dueMonth
		}
		return uuid.MustParse(str(t, e.execute(t, e.wsA, RecurringCreateTool, args), "recurring_entry_id"))
	}

	mk("Netflix", 5_590, "", 0)          // monthly
	gym := mk("Academia", 12_000, "", 0) // monthly, cancelled below
	// Annual, due in March. The summary still reports a TWELFTH of it per
	// month: this contract answers "quanto sai por mês" and is deliberately
	// untouched by the monthly-commitment reading, which puts the whole
	// amount in March and nothing in the other eleven.
	mk("Seguro", 120_000, "annual", 3) // annual → 10.000 per month

	out := e.execute(t, e.wsA, RecurringSummaryTool, nil)
	if got := num(t, out, "expense_monthly_cents"); got != 5_590+12_000+10_000 {
		t.Fatalf("total is %d, want %d", got, 5_590+12_000+10_000)
	}

	// "Cancelei a academia" — the row stays, the total drops.
	cancelled := e.execute(t, e.wsA, RecurringUpdateTool, map[string]any{
		"recurring_entry_id": gym.String(), "cancel": true,
	})
	if ok, _ := cancelled["cancelled"].(bool); !ok {
		t.Fatalf("cancel did not report itself: %v", cancelled)
	}
	if charging, _ := cancelled["active_now"].(bool); charging {
		t.Fatal("a cancelled commitment still reports as charging")
	}

	out = e.execute(t, e.wsA, RecurringSummaryTool, nil)
	if got := num(t, out, "expense_monthly_cents"); got != 5_590+10_000 {
		t.Fatalf("after cancelling, total is %d, want %d", got, 5_590+10_000)
	}
	// The record survived the cancellation — history stays answerable.
	still := e.execute(t, e.wsA, RecurringListTool, nil)
	if got := len(rows(t, still, "recurring_entries")); got != 3 {
		t.Fatalf("listing shows %d commitments after one was cancelled, want all 3", got)
	}
	var live int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.recurring_entries WHERE id = $1 AND deleted_at IS NULL`, gym).Scan(&live); err != nil {
		t.Fatalf("count: %v", err)
	}
	if live != 1 {
		t.Fatal("cancelling deleted the row instead of ending it")
	}
}

func TestRecurringAmountChangeIsVisibleBothWays(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Saúde", domain.EntryTypeExpense)
	created := e.execute(t, e.wsA, RecurringCreateTool, map[string]any{
		"description": "Academia", "amount_cents": 12_000,
		"category_id": cat.ID.String(), "due_day": 9,
	})
	id := uuid.MustParse(str(t, created, "recurring_entry_id"))

	// "A academia agora custa R$ 149,90."
	out := e.execute(t, e.wsA, RecurringUpdateTool, map[string]any{
		"recurring_entry_id": id.String(), "amount_cents": 14_990,
	})
	if num(t, out, "previous_amount_cents") != 12_000 {
		t.Fatalf("previous amount reported as %d", num(t, out, "previous_amount_cents"))
	}

	// The screens' read path sees it.
	f, err := e.svc.GetRecurringEntry(ctxFor(e.wsA), e.wsA, id)
	if err != nil || f.AmountCents != 14_990 {
		t.Fatalf("the screens read %v (err %v)", f, err)
	}
	// And a change made through the screens is visible to the capability.
	if _, err := e.svc.UpdateRecurringEntry(ctxFor(e.wsA), app.UpdateRecurringEntryInput{
		WorkspaceID: e.wsA, ID: id, AmountCents: int64Ptr(15_990),
	}); err != nil {
		t.Fatalf("update through the screens: %v", err)
	}
	listed := e.execute(t, e.wsA, RecurringListTool, map[string]any{"search": "academia"})
	got := rows(t, listed, "recurring_entries")
	if len(got) != 1 || num(t, got[0], "amount_cents") != 15_990 {
		t.Fatalf("the capability reads %v", got)
	}
}

// The commitment total and the transaction summary are separate readings and
// must not have been folded into one another.
func TestRecurringIsNotFoldedIntoTheTotalsContract(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Moradia", domain.EntryTypeExpense)
	e.execute(t, e.wsA, RecurringCreateTool, map[string]any{
		"description": "Aluguel", "amount_cents": 180_000,
		"category_id": cat.ID.String(), "due_day": 5,
	})

	// The frozen contract still reports only what is in finance.transactions.
	sum := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "this_month"})
	if got := num(t, nested(t, sum, "realized"), "expense_cents"); got != 0 {
		t.Fatalf("a commitment moved the realized bucket by %d cents", got)
	}
	if got := num(t, nested(t, sum, "projected"), "expense_cents"); got != 0 {
		t.Fatalf("a commitment moved the projected bucket by %d cents", got)
	}
	// And the separate reading has it.
	c := e.execute(t, e.wsA, RecurringSummaryTool, nil)
	if got := num(t, c, "expense_monthly_cents"); got != 180_000 {
		t.Fatalf("commitment total is %d", got)
	}
}

/* ══ isolation ════════════════════════════════════════════════════════ */

func TestNewCapabilitiesRespectWorkspaceIsolation(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Moradia", domain.EntryTypeExpense)
	card := e.seedCard(e.wsA, "Nubank", "Nubank", "1234", 1_000_000)
	person := e.execute(t, e.wsA, PersonCreateTool, map[string]any{"name": "Maria"})
	fixed := e.execute(t, e.wsA, RecurringCreateTool, map[string]any{
		"description": "Aluguel", "amount_cents": 180_000,
		"category_id": cat.ID.String(), "due_day": 5,
	})

	// B sees none of it.
	for _, probe := range []struct {
		name chatdomain.ToolName
		key  string
	}{
		{CardListTool, "cards"},
		{PersonListTool, "people"},
		{RecurringListTool, "recurring_entries"},
	} {
		out := e.execute(t, e.wsB, probe.name, nil)
		if got := len(rows(t, out, probe.key)); got != 0 {
			t.Errorf("workspace B sees %d of workspace A's %s", got, probe.key)
		}
	}
	if got := num(t, e.execute(t, e.wsB, RecurringSummaryTool, nil), "expense_monthly_cents"); got != 0 {
		t.Errorf("workspace B's commitment total includes %d cents of A's", got)
	}

	// And cannot write to A's rows.
	for _, call := range []struct {
		name chatdomain.ToolName
		args map[string]any
	}{
		{CardUpdateTool, map[string]any{"card_id": card.ID.String(), "limit_cents": 1}},
		{PersonUpdateTool, map[string]any{"person_id": str(t, person, "person_id"), "name": "X"}},
		{CategoryUpdateTool, map[string]any{"category_id": cat.ID.String(), "name": "X"}},
		{RecurringUpdateTool, map[string]any{"recurring_entry_id": str(t, fixed, "recurring_entry_id"), "amount_cents": 1}},
	} {
		if _, err := e.executeErr(e.wsB, call.name, call.args); err == nil {
			t.Errorf("workspace B wrote through %s", call.name)
		}
	}
	// A's card is untouched.
	got, _ := e.svc.GetCard(ctxFor(e.wsA), e.wsA, card.ID)
	if got.LimitCents != 1_000_000 {
		t.Fatalf("workspace A's card limit is now %d", got.LimitCents)
	}
}

/* ══ audit redaction ══════════════════════════════════════════════════ */

// A Finance capability leaves a trail that says what ran, and does not keep
// what it carried.
func TestFinancePayloadsAreRedactedInTheAuditTrail(t *testing.T) {
	e := newEnv(t)
	agentID, convID := e.newAgent(e.wsA, "Ledger")
	e.authorize(e.wsA, agentID, allFinanceTools...)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)

	e.llm.scriptToolCall(TransactionCreateTool,
		`{"category_id":"`+cat.ID.String()+`","amount_cents":8990,"description":"compras da semana"}`)
	sink := e.turn(e.wsA, convID, "registra 89,90 de mercado")
	if ev, _ := sink.finished(TransactionCreateTool); ev.Status != "ok" {
		t.Fatalf("the call did not succeed: %s", ev.ErrorCode)
	}

	var name, status string
	var args, result *string
	var redacted bool
	var round, duration int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT tool_name, status, arguments, result, redacted, round, duration_ms
		   FROM chat.tool_calls WHERE conversation_id = $1`, convID).
		Scan(&name, &status, &args, &result, &redacted, &round, &duration); err != nil {
		t.Fatalf("read audit: %v", err)
	}

	// What the trail must still answer.
	if name != string(TransactionCreateTool) || status != "ok" || round < 1 {
		t.Fatalf("the trail lost the basics: %s/%s round=%d", name, status, round)
	}
	if !redacted {
		t.Fatal("a Finance call was not marked redacted")
	}
	// What it must not keep.
	if args != nil {
		t.Fatalf("arguments were persisted: %q", *args)
	}
	if result != nil {
		t.Fatalf("result was persisted: %q", *result)
	}

	// The amount and the description are nowhere in the row.
	var whole string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT coalesce(arguments,'') || ' ' || coalesce(result,'') || ' ' || coalesce(error_message,'')
		   FROM chat.tool_calls WHERE conversation_id = $1`, convID).Scan(&whole); err != nil {
		t.Fatalf("read audit text: %v", err)
	}
	for _, secret := range []string{"8990", "89,90", "compras da semana"} {
		if strings.Contains(whole, secret) {
			t.Errorf("the audit row still contains %q", secret)
		}
	}
}

// A non-Finance capability is unaffected: redaction is a property a tool
// declares, not a blanket applied to the trail.
func TestNonConfidentialToolsStillRecordTheirPayload(t *testing.T) {
	e := newEnv(t)
	for _, def := range e.registry.Definitions() {
		if strings.HasPrefix(string(def.Name), "finance.") {
			continue
		}
		if def.Confidential {
			t.Errorf("%s declares itself confidential; only Finance should today", def.Name)
		}
	}
}

/* ══ purchase-plan installments ═══════════════════════════════════════ */

// An installment is an ordinary transaction that carries a plan id, and
// every path must treat it as one.
//
// ── The bug this pins down ─────────────────────────────────────────────
// The transaction modal routed an edit locally whenever `planId != null`.
// That was right while installments were synthesised in the browser and
// became wrong when plans moved to Postgres: the edit was written to an
// array holding no backend rows, matched nothing, and the modal closed
// reporting success. Nothing errored; the amount simply did not change.
//
// The frontend guard lives in transactionRouting.test.ts. This is the other
// half: the backend genuinely accepts the write through the same
// application call every other transaction uses, and the capability reads
// the result.
func TestPlanInstallmentIsAnOrdinaryTransactionOnEveryPath(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Compras", domain.EntryTypeExpense)

	plan, err := e.svc.CreatePurchasePlan(ctxFor(e.wsA), app.CreatePurchasePlanInput{
		WorkspaceID: e.wsA, CategoryID: cat.ID, Name: "Notebook",
		TotalAmountCents: 120_000, Installments: 4, FirstOccurredAt: e.daysAgo(30),
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// An explicit window wide enough for all four: a four-month plan
	// starting 30 days ago runs two months into the future, and every named
	// period stops at today.
	listed := e.execute(t, e.wsA, TransactionListTool, map[string]any{
		"from":   e.daysAgo(60).Format("2006-01-02"),
		"to":     e.today().AddDate(0, 0, 150).Format("2006-01-02"),
		"search": "Notebook", "limit": 50,
	})
	items := rows(t, listed, "transactions")
	if len(items) != 4 {
		t.Fatalf("the capability lists %d installments, want 4", len(items))
	}
	var target uuid.UUID
	for _, row := range items {
		if n, ok := row["installment_number"].(float64); ok && int(n) == 1 {
			target = uuid.MustParse(str(t, row, "id"))
		}
		// Every row declares which plan it belongs to, so a model can say so
		// rather than editing one silently.
		if _, ok := row["plan_id"]; !ok {
			t.Errorf("an installment row does not declare its plan")
		}
	}
	if target == uuid.Nil {
		t.Fatal("installment 1 not found in the listing")
	}

	/* ── the screens' write path ─────────────────────────────────────── */
	if _, err := e.svc.UpdateTransaction(ctxFor(e.wsA), app.UpdateTransactionInput{
		WorkspaceID: e.wsA, ID: target, AmountCents: int64Ptr(45_678),
	}); err != nil {
		t.Fatalf("the application refused an installment update: %v", err)
	}
	if stored, _ := e.rawAmount(target); stored != 45_678 {
		t.Fatalf("Postgres holds %d cents", stored)
	}
	read := e.execute(t, e.wsA, TransactionGetTool, map[string]any{"transaction_id": target.String()})
	if got := num(t, nested(t, read, "transaction"), "amount_cents"); got != 45_678 {
		t.Fatalf("the capability reads %d after the screens changed it", got)
	}

	/* ── the capability's write path ─────────────────────────────────── */
	out := e.execute(t, e.wsA, TransactionUpdateTool, map[string]any{
		"transaction_id": target.String(), "amount_cents": 50_000,
	})
	if num(t, out, "amount_cents") != 50_000 {
		t.Fatalf("update reported %d", num(t, out, "amount_cents"))
	}
	if _, ok := out["note"]; !ok {
		t.Error("updating an installment did not mention the plan")
	}
	back, err := e.svc.GetTransaction(ctxFor(e.wsA), e.wsA, target)
	if err != nil || back.AmountCents != 50_000 {
		t.Fatalf("the screens read %v (err %v)", back, err)
	}
	// The plan still has its four installments; editing one did not remove
	// or duplicate any.
	var kids int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.transactions WHERE plan_id = $1 AND deleted_at IS NULL`,
		plan.ID).Scan(&kids); err != nil {
		t.Fatalf("count installments: %v", err)
	}
	if kids != 4 {
		t.Fatalf("the plan has %d live installments after one edit", kids)
	}
}

/* ══ schema readiness: external identity ══════════════════════════════ */

// A transaction may carry the identity of the row it came from, and the
// database refuses two live rows claiming the same one.
//
// ── What this is NOT ───────────────────────────────────────────────────
// It is not an import. No capability accepts these fields, and
// `transaction.create` is unchanged — the ordinary path leaves both NULL.
// The test drives the application layer directly, which is the only writer
// that can set them today, precisely to prove the constraint is real before
// anything depends on it.
func TestTransactionExternalIdentity(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	src, id := "bank-statement", "row-4471"

	create := func(source, extID *string, desc string) error {
		_, err := e.svc.CreateTransaction(ctxFor(e.wsA), app.CreateTransactionInput{
			WorkspaceID: e.wsA, CategoryID: cat.ID, AmountCents: 1000,
			Description: desc, OccurredAt: e.daysAgo(1),
			ExternalSource: source, ExternalID: extID,
		})
		return err
	}

	if err := create(&src, &id, "first"); err != nil {
		t.Fatalf("first import row refused: %v", err)
	}
	// The same identity, twice, is a conflict rather than a second row.
	if err := create(&src, &id, "duplicate"); err == nil {
		t.Fatal("the same external identity was accepted twice")
	}
	if n := e.liveCount(e.wsA); n != 1 {
		t.Fatalf("%d rows exist after a duplicate was refused", n)
	}

	// The SAME id from a DIFFERENT source is a different thing. This is the
	// whole reason the identity is a pair.
	other := "card-export"
	if err := create(&other, &id, "same id, other source"); err != nil {
		t.Fatalf("the same id from another source was refused: %v", err)
	}

	// Ordinary transactions carry no identity and are unaffected — any
	// number of them can look alike.
	for i := 0; i < 3; i++ {
		if err := create(nil, nil, "conversational"); err != nil {
			t.Fatalf("an ordinary transaction was refused: %v", err)
		}
	}
	if n := e.liveCount(e.wsA); n != 5 {
		t.Fatalf("expected 5 live rows, got %d", n)
	}

	// Half an identity is refused by the domain before the database sees it.
	if err := create(&src, nil, "half"); err == nil {
		t.Fatal("a source without an id was accepted")
	}
	if err := create(nil, &id, "half"); err == nil {
		t.Fatal("an id without a source was accepted")
	}

	// ── Soft delete releases the identity, and that is the decision ─────
	// A deleted transaction is gone from every read, so holding its
	// identity would mean the source that produced it could never bring it
	// back. The consequence — a later import recreates it — is the honest
	// reading of a delete.
	var first uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id FROM finance.transactions WHERE external_source=$1 AND external_id=$2 AND deleted_at IS NULL`,
		src, id).Scan(&first); err != nil {
		t.Fatalf("find the imported row: %v", err)
	}
	if err := e.svc.DeleteTransaction(ctxFor(e.wsA), e.wsA, first); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := create(&src, &id, "re-imported after delete"); err != nil {
		t.Fatalf("a released identity was not reusable: %v", err)
	}

	// And the identity is per workspace: another workspace may hold the
	// same source and id without collision.
	catB := e.seedCategory(e.wsB, "Mercado", domain.EntryTypeExpense)
	if _, err := e.svc.CreateTransaction(ctxFor(e.wsB), app.CreateTransactionInput{
		WorkspaceID: e.wsB, CategoryID: catB.ID, AmountCents: 1000,
		Description: "other workspace", OccurredAt: e.daysAgo(1),
		ExternalSource: &src, ExternalID: &id,
	}); err != nil {
		t.Fatalf("another workspace was blocked by this one's identity: %v", err)
	}
}

// No capability offers the external identity — it is schema readiness, not
// a feature.
func TestNoCapabilityAcceptsExternalIdentity(t *testing.T) {
	e := newEnv(t)
	for _, def := range e.registry.Definitions() {
		if !strings.HasPrefix(string(def.Name), "finance.") {
			continue
		}
		for name := range def.Schema.Properties {
			if strings.Contains(name, "external") {
				t.Errorf("%s accepts %q; import is not a capability yet", def.Name, name)
			}
		}
	}
}

/* ══ the plan installment invariant ═══════════════════════════════════ */

// Two live rows cannot claim the same position in the same plan.
//
// `domain/transaction.go` asserted this in a comment while the schema had
// only a non-unique index — the code relied on a guarantee nothing held.
// The database holds it now, because the semantics are real: an installment
// number is a position within a plan, and a position holds one thing.
func TestPlanInstallmentPositionIsUnique(t *testing.T) {
	e := newEnv(t)
	cat := e.seedCategory(e.wsA, "Compras", domain.EntryTypeExpense)
	plan, err := e.svc.CreatePurchasePlan(ctxFor(e.wsA), app.CreatePurchasePlanInput{
		WorkspaceID: e.wsA, CategoryID: cat.ID, Name: "Notebook",
		TotalAmountCents: 120_000, Installments: 4, FirstOccurredAt: e.daysAgo(30),
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// A second row claiming installment 1 is refused by the database. The
	// insert goes through the repository because no application operation
	// offers to create a stray installment — which is the point: the
	// constraint is the thing that makes that true regardless.
	_, err = e.pool.Exec(context.Background(),
		`INSERT INTO finance.transactions
		   (workspace_id, category_id, type, amount_cents, description, occurred_at, plan_id, installment_number)
		 VALUES ($1,$2,'expense',1000,'stray',now(),$3,1)`,
		e.wsA, cat.ID, plan.ID)
	if err == nil {
		t.Fatal("a second installment 1 was accepted")
	}

	// Soft-deleted rows do not hold the position: cancelling a plan and
	// rebuilding over the same ground must not be blocked by rows nothing
	// can read.
	var first uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id FROM finance.transactions WHERE plan_id=$1 AND installment_number=1`,
		plan.ID).Scan(&first); err != nil {
		t.Fatalf("find installment 1: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE finance.transactions SET deleted_at = now() WHERE id = $1`, first); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO finance.transactions
		   (workspace_id, category_id, type, amount_cents, description, occurred_at, plan_id, installment_number)
		 VALUES ($1,$2,'expense',1000,'replacement',now(),$3,1)`,
		e.wsA, cat.ID, plan.ID); err != nil {
		t.Fatalf("a released position was not reusable: %v", err)
	}
}
