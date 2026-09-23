package tools

import (
	"context"
	"strings"
	"time"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

// Monthly commitment, as capabilities.
//
// ══════════════════════════════════════════════════════════════════════
//
//	PAID IS A RECORDED FACT ABOUT A MONTH, NOT AN INFERENCE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The failure these exist to close ───────────────────────────────────
// Before this, "quais contas faltam pagar esse mês" was answerable only
// from finance.recurring_entry.list, which knows what REPEATS and has no
// idea what was SETTLED. A model asked that question would read the
// recurrences, see rent, internet, electricity and gym, and produce a
// confident, fluent answer about money that is simply not grounded in
// anything. It is the same shape as the two incidents already on record:
// every layer behaves and the product renders prose.
//
// So paid state has a row, and these read it. Nothing infers a payment from
// transaction history, and nothing may: a transaction is money that moved,
// an occurrence is whether an obligation was settled, and the product does
// not own a rule that turns one into the other.
//
// ── Three verbs, no toggle ─────────────────────────────────────────────
// mark_paid, mark_pending, set_month_amount. There is deliberately no
// occurrence.update and no toggle: a toggle's meaning depends on a state
// the caller cannot see, so a re-sent tool call after a timeout would UNDO
// what the first one did, and neither the model nor the operator would
// have any way to notice. Every one of these is idempotent under retry.
//
// ── How a write finds its row ──────────────────────────────────────────
// By (recurring_entry_id, period), never by an occurrence id. The model has
// just read a month and holds the definition's id; an occurrence id would
// be a third opaque value to carry correctly through a turn, and the one
// thing this module has watched a model do with a half-remembered opaque
// value is invent a plausible one.

/* ── the names ───────────────────────────────────────────────────────── */

const (
	RecurringMonthTool     chatdomain.ToolName = "finance.recurring_entry.month"
	MarkPaidTool           chatdomain.ToolName = "finance.recurring_entry.mark_paid"
	MarkPendingTool        chatdomain.ToolName = "finance.recurring_entry.mark_pending"
	SetMonthAmountTool     chatdomain.ToolName = "finance.recurring_entry.set_month_amount"
	recurringEntryIDMaxLen                     = 36
)

/* ── shared arguments ────────────────────────────────────────────────── */

// periodProperty is the month argument the four capabilities share.
//
// Optional everywhere, and omitted means THE CURRENT MONTH as the Finance
// clock reads it. That is not the tool inventing a default: it is the same
// rule finance.transaction.create follows for an omitted date, and for the
// same reason. Nothing in a turn tells a model what month it is, so a month
// it supplied unprompted would be one it composed.
var periodProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "Optional. The month, as YYYY-MM, for example 2026-09. LEAVE IT OUT for the " +
		"current month: the server resolves it, which is more reliable than a month you " +
		"compose. Send it only for a month the user actually named, and work out which " +
		"month that is from the `today` any Finance read returns.",
	MaxLength: 7,
}

var recurringEntryIDProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "Required. The recurring entry's id, exactly as " +
		"finance.recurring_entry.month returned it. Never invent one and never reuse an " +
		"id from another capability: read the month first, find the obligation the user " +
		"means, and use the id beside it.",
	MaxLength: recurringEntryIDMaxLen,
}

// periodArg reads the optional month. An empty argument yields the zero
// Period, which the application service reads as "the current month".
func periodArg(args map[string]any) (domain.Period, error) {
	raw := strings.TrimSpace(argString(args, "period"))
	if raw == "" {
		return domain.Period{}, nil
	}
	p, err := domain.ParsePeriod(raw)
	if err != nil {
		return domain.Period{}, toolError(err)
	}
	return p, nil
}

// occurrenceRefArg builds the (entry, period) pair a write targets.
//
// The workspace comes from the context and there is NO argument through
// which a model could propose one — see workspaceOf.
func occurrenceRefArg(ctx context.Context, args map[string]any) (app.OccurrenceRef, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return app.OccurrenceRef{}, err
	}
	id, err := parseID(argString(args, "recurring_entry_id"), "recurring_entry_id")
	if err != nil {
		return app.OccurrenceRef{}, err
	}
	p, err := periodArg(args)
	if err != nil {
		return app.OccurrenceRef{}, err
	}
	return app.OccurrenceRef{WorkspaceID: ws, RecurringEntryID: id, Period: p}, nil
}

// occurrenceResult is what the three writes hand back.
//
// It reports the STORED state, read from what the service returned, so a
// confirmation quotes the figure that is actually in the database. The
// amount comes back both ways — see money.go — which is how an amount that
// was scaled wrongly becomes visible in the same sentence that reports
// success.
func occurrenceResult(o *domain.RecurringOccurrence, loc *time.Location) map[string]any {
	out := map[string]any{
		"recurring_entry_id": o.RecurringEntryID.String(),
		"period":             o.Period.String(),
		"due_on":             o.DueOn.Format(dateLayout),
		"status":             string(o.Status),
		"amount_estimated":   o.AmountEstimated,
		"time_zone":          loc.String(),
	}
	amountFields(out, "amount", o.AmountCents)
	if o.PaidAt != nil {
		// An INSTANT rendered as the calendar day it was in the reporting
		// zone, which is the day the operator would call it. `due_on` above
		// is already a civil date and needs no conversion.
		out["paid_on"] = o.PaidAt.In(loc).Format(dateLayout)
	}
	return out
}

/* ── finance.recurring_entry.month ───────────────────────────────────── */

type recurringMonth struct{ base }

func (recurringMonth) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   RecurringMonthTool,
		Title:  "Finance · Contas do mês",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Reports one month's recurring obligations and which of them are settled: " +
			"how much is committed, how much is already paid, how much remains, and for each " +
			"bill its amount, due date and whether it is paid, pending or overdue. " +
			"This is the ONLY source of paid state. Answers \"o que falta pagar esse mês\", " +
			"\"quais contas eu já paguei\", \"quanto ainda tenho comprometido\" and \"quanto " +
			"falta pagar em setembro\". " +
			"NEVER infer whether a bill was paid from the transactions: a transaction is money " +
			"that moved and an obligation being settled is a separate record, so a month with " +
			"no matching transaction is not an unpaid month and a payment in the ledger is not " +
			"a ticked bill. If it is not in this result, you do not know it. " +
			"Do NOT add these figures to finance.summary.get or to " +
			"finance.recurring_entry.summary. The summary reports money that MOVED in a " +
			"period; recurring_entry.summary reports what repeats per month, with an annual " +
			"entry divided by twelve; this reports what THIS MONTH owes, with an annual entry " +
			"counted whole in the month it falls due. All three are right about different " +
			"questions and adding any two of them double-counts. " +
			"The totals are computed by the server: report them as given and do not add the " +
			"lines up yourself. " +
			"A month that has not begun comes back with is_projection true: nothing in it is " +
			"recorded yet and nothing in it can be marked.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{"period": periodProperty},
		},
	}
}

func (t recurringMonth) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	p, err := periodArg(args)
	if err != nil {
		return nil, err
	}
	view, err := t.svc.GetMonthlyCommitment(ctx, app.GetMonthlyCommitmentInput{
		WorkspaceID: ws, Period: p,
	})
	if err != nil {
		return nil, toolError(err)
	}

	totals := view.Totals
	out := map[string]any{
		"period":           totals.Period.String(),
		"is_projection":    totals.Projection,
		"today":            view.Today.Format(dateLayout),
		"time_zone":        view.TimeZone,
		"occurrence_count": totals.Count,
		"paid_count":       totals.PaidCount,
		"pending_count":    totals.PendingCount,
		"estimated_count":  totals.EstimatedCount,
		"overdue_count":    totals.OverdueCount,
	}
	amountFields(out, "committed", totals.CommittedCents)
	amountFields(out, "paid", totals.PaidCents)
	amountFields(out, "remaining", totals.RemainingCents)
	amountFields(out, "estimated", totals.EstimatedCents)

	rows := make([]map[string]any, 0, len(view.Lines))
	for _, l := range view.Lines {
		o := l.Occurrence
		row := map[string]any{
			// The identity a write targets. Both halves, always, so the
			// model never has to reconstruct either.
			"recurring_entry_id": l.RecurringEntryID.String(),
			"period":             o.Period.String(),
			"description":        l.Description,
			"due_on":             o.DueOn.Format(dateLayout),
			"status":             string(o.Status),
			"overdue":            l.Overdue,
			"amount_estimated":   o.AmountEstimated,
		}
		amountFields(row, "amount", o.AmountCents)
		if l.CategoryName != "" {
			row["category"] = l.CategoryName
		}
		if o.PaidAt != nil {
			row["paid_on"] = o.PaidAt.Format(dateLayout)
		}
		rows = append(rows, row)
	}
	out["occurrences"] = rows

	if totals.Projection {
		out["note"] = "this month has not begun. These figures are projected from the " +
			"recurrences and nothing in them is recorded: no bill here can be marked paid, " +
			"and the amounts may still change."
	} else if len(rows) == 0 {
		out["note"] = "no recurring obligation falls in this month. That is a fact about what " +
			"Finance holds, not a claim that the user owes nothing."
	}

	// A definition nothing could place. Stated rather than dropped: the
	// totals above are INCOMPLETE while this is present, and a silently
	// short total is the failure mode this whole module is arranged
	// against.
	if len(view.Unplaceable) > 0 {
		missing := make([]map[string]any, 0, len(view.Unplaceable))
		for _, u := range view.Unplaceable {
			missing = append(missing, map[string]any{
				"recurring_entry_id": u.RecurringEntryID.String(),
				"description":        u.Description,
				"reason":             string(u.Reason),
			})
		}
		out["unplaceable"] = missing
		out["unplaceable_note"] = "these annual recurring entries do not say WHICH MONTH they " +
			"fall due, so they are in no month and are NOT counted in the totals above. The " +
			"totals are therefore incomplete. Tell the user, ask which month each one is due, " +
			"and set it with finance.recurring_entry.update. Do not guess a month."
	}
	return fit(out, "occurrences")
}

/* ── finance.recurring_entry.mark_paid ───────────────────────────────── */

type markPaid struct{ base }

func (markPaid) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MarkPaidTool,
		Title:        "Finance · Marcar conta como paga",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Marks ONE month of ONE recurring obligation as settled: \"paguei a " +
			"internet\", \"o aluguel já foi\", \"quitei a academia esse mês\". " +
			"Call finance.recurring_entry.month FIRST and take the recurring_entry_id from " +
			"it. If more than one obligation could be the one the user means — two bills whose " +
			"descriptions both fit what they said — ASK WHICH. Do not pick the closest match " +
			"and do not pick the first: marking the wrong bill paid leaves the real one " +
			"looking settled, and nothing will ever flag it. " +
			"This records that an OBLIGATION was settled. It creates NO transaction and no " +
			"money moves: if the user also wants the payment in the ledger, that is " +
			"finance.transaction.create and it is a separate, explicit act. " +
			"It does not change the amount. If the user says what it cost as well " +
			"(\"paguei a luz, veio 437,20\"), set the amount with " +
			"finance.recurring_entry.set_month_amount first and then mark it paid. " +
			"Marking an already-paid month again is harmless and changes nothing, so a retry " +
			"is safe. It does NOT toggle back to pending — to undo a mistake, use " +
			"finance.recurring_entry.mark_pending.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"recurring_entry_id": recurringEntryIDProperty,
				"period":             periodProperty,
			},
			Required: []string{"recurring_entry_id"},
		},
	}
}

func (t markPaid) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ref, err := occurrenceRefArg(ctx, args)
	if err != nil {
		return nil, err
	}
	o, err := t.svc.MarkOccurrencePaid(ctx, ref)
	if err != nil {
		return nil, toolError(err)
	}
	out := occurrenceResult(o, t.loc)
	out["marked_paid"] = true
	out["note"] = "this records that the obligation was settled. No transaction was created " +
		"and no money was recorded as having moved."
	return out, nil
}

/* ── finance.recurring_entry.mark_pending ────────────────────────────── */

type markPending struct{ base }

func (markPending) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MarkPendingTool,
		Title:        "Finance · Desmarcar conta paga",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Undoes a mistaken payment mark on ONE month of ONE recurring " +
			"obligation: \"marquei errado, a internet ainda não foi paga\", \"o aluguel não " +
			"foi pago ainda\". The bill goes back to pending. " +
			"Call finance.recurring_entry.month first and take the recurring_entry_id from " +
			"it; if more than one obligation could be meant, ask which. " +
			"It does NOT delete any transaction and does NOT change the amount. Being wrong " +
			"about whether a bill was paid says nothing about whether the figure was right, " +
			"and money that really moved stays in the ledger. " +
			"Doing this to an already-pending month is harmless and changes nothing, so a " +
			"retry is safe.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"recurring_entry_id": recurringEntryIDProperty,
				"period":             periodProperty,
			},
			Required: []string{"recurring_entry_id"},
		},
	}
}

func (t markPending) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ref, err := occurrenceRefArg(ctx, args)
	if err != nil {
		return nil, err
	}
	o, err := t.svc.UnmarkOccurrencePaid(ctx, ref)
	if err != nil {
		return nil, toolError(err)
	}
	out := occurrenceResult(o, t.loc)
	out["marked_pending"] = true
	out["note"] = "no transaction was deleted and the amount was left as it was."
	return out, nil
}

/* ── finance.recurring_entry.set_month_amount ────────────────────────── */

type setMonthAmount struct{ base }

func (setMonthAmount) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SetMonthAmountTool,
		Title:        "Finance · Valor da conta no mês",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Records what ONE month of a recurring obligation actually cost, for the " +
			"bills whose amount changes: \"a luz desse mês veio 437,20\", \"a água deu 98,40\", " +
			"\"a fatura fechou em 1.240\". The figure stops being an estimate. " +
			"This is about THIS MONTH only. It does NOT change the recurrence's usual amount: " +
			"\"a luz veio 437,20\" is a fact about one bill, while \"a academia agora custa " +
			"149,90 todo mês\" is a change to the recurrence and is " +
			"finance.recurring_entry.update. " +
			"It does NOT mark the bill paid. If the user said they also paid it, call " +
			"finance.recurring_entry.mark_paid afterwards — two facts, two capabilities, so " +
			"the record shows which of them actually happened. " +
			"A month already marked paid is REFUSED rather than quietly rewritten: mark it " +
			"pending, set the amount, then mark it paid again.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"recurring_entry_id": recurringEntryIDProperty,
				"period":             periodProperty,
				"amount_cents": {
					Type: chatdomain.TypeInteger,
					Description: "Required. What this month's bill cost, in CENTS, as a whole " +
						"number, always positive. R$ 437,20 is 43720. R$ 98,40 is 9840. " +
						"R$ 1.240,00 is 124000. Multiply by 100 and never send a decimal: " +
						"437.20 is rejected, and sending 437 would record four reais e trinta " +
						"e sete centavos.",
				},
			},
			Required: []string{"recurring_entry_id", "amount_cents"},
		},
	}
}

func (t setMonthAmount) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ref, err := occurrenceRefArg(ctx, args)
	if err != nil {
		return nil, err
	}
	amount := argInt64Ptr(args, "amount_cents")
	if amount == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"amount_cents is required and must be a whole number of cents")
	}
	o, err := t.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: ref, AmountCents: *amount,
	})
	if err != nil {
		return nil, toolError(err)
	}
	out := occurrenceResult(o, t.loc)
	out["amount_set"] = true
	out["note"] = "only this month was changed. The recurrence's usual amount is unchanged, " +
		"and the bill was NOT marked paid."
	return out, nil
}
