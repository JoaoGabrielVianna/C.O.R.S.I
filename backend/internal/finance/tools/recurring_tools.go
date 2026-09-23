package tools

import (
	"context"
	"strings"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

// Recurring entries, as capabilities.
//
// ── Why these are not transaction tools with a flag ────────────────────
// Because the two answer different questions and must never be added
// together by accident. A transaction is money that moved. A recurring
// entry is a statement that something repeats — it has a due DAY, not a
// date, and no end until someone ends it. Recording one writes nothing to
// the ledger, and this package has no way to make it: the application
// service it calls does not touch `finance.transactions` at all.
//
// ── Why one concept covers income and expense ──────────────────────────
// Because a salary repeats exactly the way a rent does, and the direction
// is a property of the CATEGORY, not of the recurrence. Two capabilities
// would be two names for one thing, and the model would have to guess
// which one a sentence belonged to.
//
// The distinction survives contact with a model because it is in the
// description of every tool below, in the name of the capability, and in
// the fact that the two live in different tables.

/* ── finance.recurring_entry.list ────────────────────────────────────── */

type recurringList struct{ base }

func (recurringList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   RecurringListTool,
		Title:  "Finance · Listar recorrentes",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Lists what repeats every month or every year — rent, subscriptions, a " +
			"gym, insurance, AND recurring income such as a salary — with the amount, the day " +
			"of the month it falls due, how often it repeats, its direction (income or " +
			"expense) and whether it is running now. Answers \"quais são meus gastos fixos\", " +
			"\"o que eu pago todo mês\" and \"quais minhas receitas recorrentes\". " +
			"These are RECURRENCES, not payments: saying something repeats does not mean the " +
			"money moved. What actually moved is in the transactions, and the two are counted " +
			"separately — adding a recurrence to a month's spending would double-count the " +
			"months it was already paid in. " +
			"For the totals per month, use finance.recurring_entry.summary, which reports " +
			"income and expense apart, normalises annual entries and excludes paused and " +
			"ended ones.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"active_only": {
					Type: chatdomain.TypeBoolean,
					Description: "Optional. True returns only what is being charged right now. " +
						"Defaults to false, which also shows paused and ended entries — " +
						"needed to answer \"o que eu cancelei\".",
				},
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Matches the description, case-insensitively.",
					MaxLength:   120,
				},
			},
		},
	}
}

func (t recurringList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	clk, err := t.clock(ctx)
	if err != nil {
		return nil, err
	}
	activeOnly, _ := args["active_only"].(bool)
	items, err := t.svc.ListRecurringEntries(ctx, app.ListRecurringEntriesInput{
		WorkspaceID: ws, ActiveOnly: activeOnly, Limit: 200,
	})
	if err != nil {
		return nil, toolError(err)
	}
	needle := strings.ToLower(strings.TrimSpace(argString(args, "search")))
	cats := t.categoryIndex(ctx, ws)
	now := clk.now()
	rows := make([]map[string]any, 0, len(items))
	for i := range items {
		f := items[i]
		if needle != "" && !strings.Contains(strings.ToLower(f.Description), needle) {
			continue
		}
		rows = append(rows, recurringRow(&f, cats, now))
	}
	out := map[string]any{"recurring_entries": rows, "today": clk.today().Format(dateLayout)}
	if len(rows) == 0 {
		out["note"] = "no recurring entry matched. This does not mean the user has none — it " +
			"may be that none is recorded in Finance yet."
	}
	return fit(out, "recurring_entries")
}

/* ── finance.recurring_entry.create ──────────────────────────────────── */

type recurringCreate struct{ base }

func (recurringCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   RecurringCreateTool,
		Title:  "Finance · Registrar recorrente",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Records something that REPEATS every month or every year, in either " +
			"direction. Use it for \"pago 1.800 de aluguel todo mês\", \"assinei a Netflix\" " +
			"— and equally for income: \"recebo 8 mil de salário todo mês\", \"minha " +
			"aposentadoria cai dia 5\". The direction comes from the category you choose, so " +
			"a salary goes under an income category and a rent under an expense one. " +
			"This does NOT record a payment or a receipt. It creates no transaction, and it " +
			"must not be used for money that just moved — \"paguei o aluguel hoje\" and " +
			"\"caiu o salário\" are finance.transaction.create. If the user reports both at " +
			"once (\"caiu o salário, são 8 mil todo mês\"), those are two different facts and " +
			"each has its own capability. " +
			"Do not record something the user is only considering. \"Estou pensando em " +
			"assinar\" and \"queria fazer academia\" are plans, and a recurrence recorded from " +
			"one distorts every figure about what repeats each month.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"description": {
					Type:        chatdomain.TypeString,
					Description: "What this is: \"Aluguel\", \"Netflix\", \"Salário\". Required.",
					MaxLength:   280,
				},
				"amount_cents": {
					Type: chatdomain.TypeInteger,
					Description: "Required. The recurring amount in CENTS, whole number, always " +
						"positive — the direction comes from the category, not from a sign. " +
						"R$ 1.800 is 180000. R$ 149,90 is 14990. Never a decimal.",
				},
				"category_id": {
					Type: chatdomain.TypeString,
					Description: "Required. A category id from finance.category.list. It decides " +
						"the DIRECTION: an income category makes this recurring income, an expense " +
						"category makes it a recurring expense. Picking the wrong one inverts the " +
						"sign of everything this entry contributes.",
					MaxLength: 36,
				},
				"due_day": {
					Type:        chatdomain.TypeInteger,
					Description: "Required. Day of the month it falls due, 1 to 31.",
				},
				"recurrence": {
					Type: chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(domain.RecurrenceNames(), ", ") +
						". Defaults to monthly.",
					MaxLength: 10,
				},
				// ── Why an annual entry cannot be recorded without this ──
				// Because "annual" alone does not say WHEN. An IPVA is due
				// in January whether it was written down in January or in
				// September, and there is nothing in a recurrence or a
				// start date that distinguishes the two. Left to be
				// inferred it would land in the month of its own data
				// entry, and a yearly bill in the wrong month is a total
				// that is wrong twice: too high in one month, too low in
				// another, both by the whole amount.
				"due_month": {
					Type: chatdomain.TypeInteger,
					Description: "Required when recurrence is 'annual', and must not be sent " +
						"otherwise. The MONTH it falls due, 1 for January to 12 for December. " +
						"It is not the month the user is telling you about it: \"pago IPVA todo " +
						"ano\" said in September is due_month 1 if the user says January. If they " +
						"have not said which month, ASK — never infer it from today's date or " +
						"from when the record is being created.",
				},
				"notes": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only what the user actually said.",
					MaxLength:   1000,
				},
			},
			Required: []string{"description", "amount_cents", "category_id", "due_day"},
		},
	}
}

func (t recurringCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	catID, err := parseID(argString(args, "category_id"), "category_id")
	if err != nil {
		return nil, err
	}
	amount := argInt64Ptr(args, "amount_cents")
	if amount == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"amount_cents is required and must be a whole number of cents")
	}
	dueDay := argIntPtr(args, "due_day")
	if dueDay == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"due_day is required and must be a whole number from 1 to 31")
	}
	in := app.CreateRecurringEntryInput{
		WorkspaceID: ws, Description: argString(args, "description"),
		AmountCents: *amount, CategoryID: catID, DueDay: *dueDay,
		DueMonth: argIntPtr(args, "due_month"),
	}
	if raw := strings.TrimSpace(argString(args, "recurrence")); raw != "" {
		r, err := domain.ParseRecurrence(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Recurrence = &r
	}
	if n := argStringPtr(args, "notes"); n != nil {
		in.Notes = n
	}

	f, err := t.svc.CreateRecurringEntry(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{
		"recurring_entry_id": f.ID.String(),
		"description":        f.Description,
		"due_day":            f.DueDay,
		"recurrence":         string(f.Recurrence),
		"created":            true,
		// Stated in the result, not only in the description, because this is
		// the moment a model is most likely to also "record the payment".
		"note": "this is a recurring entry. No transaction was created, and none should be " +
			"unless the user says money actually moved.",
	}
	amountFields(out, "amount", f.AmountCents)
	if cat, err := t.svc.GetCategory(ctx, ws, f.CategoryID); err == nil {
		out["category"] = cat.Name
	}
	return out, nil
}

/* ── finance.recurring_entry.update ──────────────────────────────────── */

type recurringUpdate struct{ base }

func (recurringUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   RecurringUpdateTool,
		Title:  "Finance · Atualizar recorrente",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Changes a recurring entry, or ends one. Use it for \"a academia agora " +
			"custa 149,90\", \"o aluguel subiu\", \"tive aumento, agora recebo 9 mil\", " +
			"\"mudou o vencimento para dia 10\", and for \"cancelei a academia\". " +
			"CANCELLING is `cancel: true`, not a removal. The entry stays on record with the " +
			"date it ended, so a question about what the user was paying — or earning — last " +
			"March still has a true answer; it simply stops counting from now on. " +
			"PAUSING is different: use `status` for something expected to resume. " +
			"\"Estou pensando em cancelar\" is not a cancellation. Change nothing until the " +
			"user says it happened. " +
			"Resolve WHICH entry first with finance.recurring_entry.list, and ask if more " +
			"than one could match.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"recurring_entry_id": {
					Type:        chatdomain.TypeString,
					Description: "The entry's id, from finance.recurring_entry.list. Never invent one.",
					MaxLength:   36,
				},
				"amount_cents": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional. The new recurring amount in CENTS, positive. R$ 149,90 is 14990.",
				},
				"description": {
					Type:        chatdomain.TypeString,
					Description: "Optional. A corrected description.",
					MaxLength:   280,
				},
				"due_day": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional. New day of the month it falls due, 1 to 31.",
				},
				"due_month": {
					Type: chatdomain.TypeInteger,
					Description: "Optional, and only for an annual entry: the MONTH it falls due, " +
						"1 to 12. It must be set when an entry becomes annual, and it must be " +
						"absent on a monthly one. If the user has not said which month, ask.",
				},
				"category_id": {
					Type: chatdomain.TypeString,
					Description: "Optional. A different category, by id. Moving an entry to a " +
						"category of the other direction flips it between income and expense, so " +
						"only do it when that is what the user meant.",
					MaxLength: 36,
				},
				"status": {
					Type: chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(domain.RecurringStatusNames(), ", ") +
						". Use 'paused' for something temporarily not charged but expected back. " +
						"To END an entry for good, use `cancel` instead.",
					MaxLength: 10,
				},
				"cancel": {
					Type: chatdomain.TypeBoolean,
					Description: "Optional. True ends the entry as of today: it stops counting " +
						"towards the monthly total and stays on record. Use it when the user says " +
						"they cancelled something.",
				},
				"reactivate": {
					Type: chatdomain.TypeBoolean,
					Description: "Optional. True undoes a cancellation — for an entry the user " +
						"resumed, or one cancelled by mistake.",
				},
			},
			Required: []string{"recurring_entry_id"},
		},
	}
}

func (t recurringUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "recurring_entry_id"), "recurring_entry_id")
	if err != nil {
		return nil, err
	}
	clk, err := t.clock(ctx)
	if err != nil {
		return nil, err
	}
	before, err := t.svc.GetRecurringEntry(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	cancel, _ := args["cancel"].(bool)
	reactivate, _ := args["reactivate"].(bool)
	if cancel && reactivate {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"cancel and reactivate contradict each other; send one of them")
	}

	in := app.UpdateRecurringEntryInput{
		WorkspaceID: ws, ID: id,
		AmountCents: argInt64Ptr(args, "amount_cents"),
		Description: argStringPtr(args, "description"),
		DueDay:      argIntPtr(args, "due_day"),
		// Only the month moves here, never the frequency: this tool has no
		// `recurrence` argument and never had one, so an entry cannot
		// change between monthly and annual through a conversation. That
		// keeps `due_month` a single-meaning field on this path — it can be
		// corrected, and it can never be left stranded on an entry that
		// stopped being annual.
		DueMonth: argIntPtr(args, "due_month"),
	}
	if raw := argStringPtr(args, "category_id"); raw != nil {
		cid, err := parseID(*raw, "category_id")
		if err != nil {
			return nil, err
		}
		in.CategoryID = &cid
	}
	if raw := argStringPtr(args, "status"); raw != nil {
		st, err := domain.ParseRecurringStatus(*raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Status = &st
	}
	if cancel {
		// Ended as of now, from the same clock the rest of this context
		// reckons by.
		now := clk.now()
		in.EndsAt = &now
	}
	if reactivate {
		in.ClearEndsAt = true
		active := domain.StatusActive
		in.Status = &active
	}

	f, err := t.svc.UpdateRecurringEntry(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"recurring_entry_id": f.ID.String(),
		"description":        f.Description,
		"due_day":            f.DueDay,
		"recurrence":         string(f.Recurrence),
		"status":             string(f.Status),
		"charging_now":       f.ActiveAt(clk.now()),
		"updated":            true,
	}
	amountFields(out, "amount", f.AmountCents)
	if in.AmountCents != nil && before.AmountCents != f.AmountCents {
		amountFields(out, "previous_amount", before.AmountCents)
	}
	if f.EndsAt != nil {
		out["ends_on"] = f.EndsAt.In(t.loc).Format(dateLayout)
		out["cancelled"] = true
		out["note"] = "the entry was kept on record and stops counting towards the monthly totals."
	}
	return out, nil
}

/* ── finance.recurring_entry.summary ─────────────────────────────────── */

type recurringSummaryGet struct{ base }

func (recurringSummaryGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   RecurringSummaryTool,
		Title:  "Finance · Resumo dos recorrentes",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Totals what repeats every month, income and expense reported APART, " +
			"with annual entries divided by twelve so the figures are comparable. Answers " +
			"\"quanto tenho de receitas e despesas recorrentes por mês\", \"quanto comprometo " +
			"por mês\" and belongs in any answer about how much the user can save. " +
			"This is SEPARATE from finance.summary.get and the two must not be added " +
			"together. The summary reports money that MOVED in a period; this reports what " +
			"REPEATS. A recurrence already paid this month appears in BOTH, and summing them " +
			"would count it twice — present them side by side instead. " +
			"Paused and ended entries are excluded.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"include_lines": {
					Type: chatdomain.TypeBoolean,
					Description: "Optional. True also returns each entry making up the totals. " +
						"Defaults to true; send false when only the figures are needed.",
				},
			},
		},
	}
}

func (t recurringSummaryGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	c, err := t.svc.GetRecurringSummary(ctx, ws)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{"entry_count": len(c.Lines)}
	// Both directions, reported apart. A single net figure would hide the
	// case that matters most: R$ 8.000 in and R$ 7.900 out nets the same as
	// R$ 200 in and R$ 100 out.
	amountFields(out, "income_monthly", c.IncomeMonthlyCents)
	amountFields(out, "expense_monthly", c.ExpenseMonthlyCents)
	amountFields(out, "net_monthly", c.NetMonthlyCents)
	out["as_of"] = c.At.In(t.loc).Format(dateLayout)

	includeLines := true
	if v, ok := args["include_lines"].(bool); ok {
		includeLines = v
	}
	if includeLines {
		cats := t.categoryIndex(ctx, ws)
		rows := make([]map[string]any, 0, len(c.Lines))
		for _, l := range c.Lines {
			row := map[string]any{
				"id":          l.Entry.ID.String(),
				"description": l.Entry.Description,
				"direction":   string(l.Direction),
				"due_day":     l.Entry.DueDay,
				"recurrence":  string(l.Entry.Recurrence),
			}
			amountFields(row, "monthly", l.MonthlyCents)
			if l.Entry.Recurrence == domain.RecurrenceAnnual {
				amountFields(row, "annual_amount", l.Entry.AmountCents)
			}
			if cat, ok := cats[l.Entry.CategoryID]; ok {
				row["category"] = cat.Name
			}
			rows = append(rows, row)
		}
		out["entries"] = rows
	}
	if len(c.Lines) == 0 {
		out["note"] = "no active recurring entry is recorded. That is a fact about what " +
			"Finance holds, not a claim that the user has none."
	}
	return fit(out, "entries")
}
