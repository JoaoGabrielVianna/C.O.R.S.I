// Domain coverage beyond transactions: the vocabulary and the commitments.
//
// ── Why these are separate tools and not arguments on existing ones ────
// Because a capability is the unit an operator grants and revokes. An agent
// that may record spending is not automatically an agent that may rename
// the user's categories or change a credit limit, and collapsing those into
// one tool with a `resource` argument would make the grant meaningless —
// the interface would be offering one switch for four different powers.
//
// ── What is deliberately absent, and why ───────────────────────────────
// No delete for Category or Person. Both application operations are
// unguarded soft deletes: nothing checks whether transactions still
// reference the row, and the listing filters deleted rows out, so removing
// a category in use leaves historical transactions pointing at a label
// nothing can resolve. That is the real semantics of the domain today, and
// the honest response is not to invent a guard here — it is to not hand a
// model the operation.
//
// No archive for Card either, for the same reason one level down: the
// screens keep a localStorage shadow of archived cards precisely so old
// transactions can still resolve a name, which is the symptom of the same
// unresolved question.
//
// No create for Card. A card has eight fields, several of which a person
// reads off a piece of plastic (last four digits, closing day, due day,
// network). A model filling those from conversation would be guessing at
// the parts that matter, and the screens already do it well.
package tools

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

const (
	CategoryCreateTool chatdomain.ToolName = "finance.category.create"
	CategoryUpdateTool chatdomain.ToolName = "finance.category.update"
	CardListTool       chatdomain.ToolName = "finance.card.list"
	CardUpdateTool     chatdomain.ToolName = "finance.card.update"
	PersonListTool     chatdomain.ToolName = "finance.person.list"
	PersonCreateTool   chatdomain.ToolName = "finance.person.create"
	PersonUpdateTool   chatdomain.ToolName = "finance.person.update"
	// ── Why these are `recurring_entry` and not `fixed_expense` ────────
	// Because the concept holds a salary as readily as a rent, and a
	// capability named after only one direction would be a contract this
	// module has to live with. Renaming a tool revokes every grant that
	// referenced the old name — see chat/domain/tool.go — which is exactly
	// why it is done before the first release and not after.
	RecurringListTool    chatdomain.ToolName = "finance.recurring_entry.list"
	RecurringCreateTool  chatdomain.ToolName = "finance.recurring_entry.create"
	RecurringUpdateTool  chatdomain.ToolName = "finance.recurring_entry.update"
	RecurringSummaryTool chatdomain.ToolName = "finance.recurring_entry.summary"
)

// defaultCategoryColor / defaultCategoryIcon are what a conversationally
// created category gets.
//
// The domain requires both, and neither is financial data — they are how a
// screen draws a row. Asking the user to choose a hex colour mid-sentence
// would be the tool making its own schema the user's problem, and letting
// the model invent one is fine precisely because nothing depends on it.
const (
	defaultCategoryColor = "#64748b"
	defaultCategoryIcon  = "tag"
)

/* ── finance.category.create ─────────────────────────────────────────── */

type categoryCreate struct{ base }

func (categoryCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   CategoryCreateTool,
		Title:  "Finance · Criar categoria",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Creates a spending or income category. Use it when the user asks for " +
			"one — \"cria uma categoria Viagens\" — or when they want to record something " +
			"that genuinely has no category yet and they agreed to add it. " +
			"Check finance.category.list first: categories are the user's own vocabulary " +
			"and near-duplicates (\"Mercado\" beside \"Supermercado\") make every later " +
			"total ambiguous. Do NOT create one just to make a transaction fit — ask which " +
			"existing category they meant.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"name": {
					Type:        chatdomain.TypeString,
					Description: "The category name, as the user said it. Required.",
					MaxLength:   80,
				},
				"type": {
					Type: chatdomain.TypeString,
					Description: "Required. One of: " + strings.Join(entryTypeNames(), ", ") +
						". This decides the direction of every transaction filed under it, so " +
						"a spending category must be 'expense'.",
					MaxLength: 10,
				},
			},
			Required: []string{"name", "type"},
		},
	}
}

func (t categoryCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	ty, err := parseEntryType(argString(args, "type"))
	if err != nil {
		return nil, err
	}
	cat, err := t.svc.CreateCategory(ctx, app.CreateCategoryInput{
		WorkspaceID: ws, Name: argString(args, "name"), Type: ty,
		Color: defaultCategoryColor, Icon: defaultCategoryIcon,
	})
	if err != nil {
		return nil, toolError(err)
	}
	return map[string]any{
		"category_id": cat.ID.String(),
		"name":        cat.Name,
		"type":        string(cat.Type),
		"created":     true,
	}, nil
}

/* ── finance.category.update ─────────────────────────────────────────── */

type categoryUpdate struct{ base }

func (categoryUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   CategoryUpdateTool,
		Title:  "Finance · Renomear categoria",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Renames a category. Every transaction already filed under it keeps its " +
			"place — this changes the label, not what is in it. " +
			"A category's DIRECTION cannot be changed and is not offered here: flipping an " +
			"expense category to income would silently reverse the sign of every " +
			"transaction filed under it. If the user needs the other direction, that is a " +
			"new category. " +
			"Removing a category is not among your capabilities: transactions reference it, " +
			"and removing one they point at would leave the history unreadable. Say so " +
			"plainly if asked.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"category_id": {
					Type:        chatdomain.TypeString,
					Description: "The category's id, from finance.category.list. Never invent one.",
					MaxLength:   36,
				},
				"name": {
					Type:        chatdomain.TypeString,
					Description: "The new name. Required.",
					MaxLength:   80,
				},
			},
			Required: []string{"category_id", "name"},
		},
	}
}

func (t categoryUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "category_id"), "category_id")
	if err != nil {
		return nil, err
	}
	before, err := t.svc.GetCategory(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	name := argString(args, "name")
	cat, err := t.svc.UpdateCategory(ctx, app.UpdateCategoryInput{
		WorkspaceID: ws, ID: id, Name: &name,
	})
	if err != nil {
		return nil, toolError(err)
	}
	return map[string]any{
		"category_id":   cat.ID.String(),
		"name":          cat.Name,
		"previous_name": before.Name,
		"type":          string(cat.Type),
		"updated":       true,
	}, nil
}

/* ── finance.card.list ───────────────────────────────────────────────── */

type cardList struct{ base }

func (cardList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   CardListTool,
		Title:  "Finance · Listar cartões",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Lists the user's credit cards with the details Finance stores: name, " +
			"institution, network, last four digits, credit limit, the day the bill closes " +
			"and the day it is due. Answers \"quantos cartões eu tenho\", \"qual o limite do " +
			"Nubank\", \"qual cartão fecha primeiro\". " +
			"Finance does NOT store a balance, an available limit, or the current bill for a " +
			"card. If asked for those, say they are not recorded rather than estimating them " +
			"from spending — a guess about how much credit is left is the kind of number " +
			"someone acts on.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Narrows by name or institution, case-insensitively.",
					MaxLength:   80,
				},
			},
		},
	}
}

func (t cardList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	cards, err := t.svc.ListCards(ctx, app.ListCardsInput{WorkspaceID: ws, Limit: 100})
	if err != nil {
		return nil, toolError(err)
	}
	needle := strings.ToLower(strings.TrimSpace(argString(args, "search")))
	rows := make([]map[string]any, 0, len(cards))
	for i := range cards {
		c := cards[i]
		if needle != "" &&
			!strings.Contains(strings.ToLower(c.Name), needle) &&
			!strings.Contains(strings.ToLower(c.Institution), needle) {
			continue
		}
		row := map[string]any{
			"id":          c.ID.String(),
			"name":        c.Name,
			"institution": c.Institution,
			"network":     string(c.Network),
			"last4":       c.Last4,
			"closing_day": c.ClosingDay,
			"due_day":     c.DueDay,
		}
		amountFields(row, "limit", c.LimitCents)
		rows = append(rows, row)
	}
	out := map[string]any{"cards": rows}
	if len(rows) == 0 {
		out["note"] = "no card matched. Finance may simply have none recorded."
	}
	// Said once, here, rather than inferred: a model that sees `limit` and
	// no balance will otherwise be tempted to compute one.
	out["not_recorded"] = "Finance does not store card balances, available limit, or the current bill."
	return fit(out, "cards")
}

/* ── finance.card.update ─────────────────────────────────────────────── */

type cardUpdate struct{ base }

func (cardUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   CardUpdateTool,
		Title:  "Finance · Atualizar cartão",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Updates what Finance records about a card: its credit limit, the day " +
			"the bill closes, the day it is due, or its name. Use it when the user reports a " +
			"real change — \"meu limite do Nubank subiu para 18 mil\", \"o vencimento mudou " +
			"para dia 10\". " +
			"Resolve WHICH card first with finance.card.list. If more than one card could be " +
			"the one they mean — two cards from the same bank, say — ask before changing " +
			"anything: there is no way for the user to see that the wrong card was edited. " +
			"A limit the user is merely hoping for, or asking you to imagine, is not a " +
			"change. Removing a card is not among your capabilities.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"card_id": {
					Type:        chatdomain.TypeString,
					Description: "The card's id, from finance.card.list. Never invent one.",
					MaxLength:   36,
				},
				"limit_cents": {
					Type: chatdomain.TypeInteger,
					Description: "Optional. The new credit limit in CENTS, whole number. " +
						"R$ 18.000 is 1800000. Never a decimal.",
				},
				"closing_day": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional. Day of the month the bill closes, 1 to 28.",
				},
				"due_day": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional. Day of the month the bill is due, 1 to 28.",
				},
				"name": {
					Type:        chatdomain.TypeString,
					Description: "Optional. A new display name for the card.",
					MaxLength:   80,
				},
			},
			Required: []string{"card_id"},
		},
	}
}

func (t cardUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "card_id"), "card_id")
	if err != nil {
		return nil, err
	}
	before, err := t.svc.GetCard(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	in := app.UpdateCardInput{WorkspaceID: ws, ID: id}
	in.LimitCents = argInt64Ptr(args, "limit_cents")
	if v := argIntPtr(args, "closing_day"); v != nil {
		in.ClosingDay = v
	}
	if v := argIntPtr(args, "due_day"); v != nil {
		in.DueDay = v
	}
	if v := argStringPtr(args, "name"); v != nil {
		in.Name = v
	}
	if in.LimitCents == nil && in.ClosingDay == nil && in.DueDay == nil && in.Name == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"nothing to change: send at least one of limit_cents, closing_day, due_day or name")
	}

	card, err := t.svc.UpdateCard(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{
		"card_id":     card.ID.String(),
		"name":        card.Name,
		"closing_day": card.ClosingDay,
		"due_day":     card.DueDay,
		"updated":     true,
	}
	amountFields(out, "limit", card.LimitCents)
	// The previous limit, when it moved: a confirmation the user can check
	// rather than one they have to trust.
	if in.LimitCents != nil && before.LimitCents != card.LimitCents {
		amountFields(out, "previous_limit", before.LimitCents)
	}
	return out, nil
}

/* ── finance.person.list ─────────────────────────────────────────────── */

type personList struct{ base }

func (personList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PersonListTool,
		Title:  "Finance · Listar pessoas",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Lists the people recorded in Finance — the household or group whose " +
			"money this workspace tracks. Answers \"quantas pessoas tenho cadastradas\" and " +
			"\"quem está cadastrado\", and is where you find the id before recording " +
			"anything against a person.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Narrows by name, case-insensitively.",
					MaxLength:   80,
				},
			},
		},
	}
}

func (t personList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	people, err := t.svc.ListPersons(ctx, app.ListPersonsInput{WorkspaceID: ws, Limit: 100})
	if err != nil {
		return nil, toolError(err)
	}
	needle := strings.ToLower(strings.TrimSpace(argString(args, "search")))
	rows := make([]map[string]any, 0, len(people))
	for _, p := range people {
		if needle != "" && !strings.Contains(strings.ToLower(p.Name), needle) {
			continue
		}
		row := map[string]any{"id": p.ID.String(), "name": p.Name}
		if p.Notes != "" {
			row["notes"] = p.Notes
		}
		rows = append(rows, row)
	}
	out := map[string]any{"people": rows, "count": len(rows)}
	if len(rows) == 0 {
		out["note"] = "no person matched. Finance may simply have none recorded."
	}
	return fit(out, "people")
}

/* ── finance.person.create ───────────────────────────────────────────── */

type personCreate struct{ base }

func (personCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PersonCreateTool,
		Title:  "Finance · Adicionar pessoa",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Adds a person to Finance. Use it when the user asks — \"adiciona a " +
			"Maria\" — so that spending can be attributed to them. " +
			"Check finance.person.list first: adding someone who is already recorded under a " +
			"slightly different spelling splits their history in two, and nothing later " +
			"joins it back. Do NOT add someone merely because they were mentioned in " +
			"conversation.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"name": {
					Type:        chatdomain.TypeString,
					Description: "The person's name, as the user said it. Required.",
					MaxLength:   80,
				},
				"notes": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only what the user actually told you.",
					MaxLength:   1000,
				},
			},
			Required: []string{"name"},
		},
	}
}

func (t personCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	in := app.CreatePersonInput{WorkspaceID: ws, Name: argString(args, "name")}
	if n := argStringPtr(args, "notes"); n != nil {
		in.Notes = *n
	}
	p, err := t.svc.CreatePerson(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}
	return map[string]any{"person_id": p.ID.String(), "name": p.Name, "created": true}, nil
}

/* ── finance.person.update ───────────────────────────────────────────── */

type personUpdate struct{ base }

func (personUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PersonUpdateTool,
		Title:  "Finance · Atualizar pessoa",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Renames a person, or updates the note kept about them. Everything " +
			"already attributed to them stays attributed — this changes the label, not the " +
			"history. " +
			"Removing a person is not among your capabilities: transactions reference them, " +
			"and removing one would leave those records pointing at nobody.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"person_id": {
					Type:        chatdomain.TypeString,
					Description: "The person's id, from finance.person.list. Never invent one.",
					MaxLength:   36,
				},
				"name": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The corrected name.",
					MaxLength:   80,
				},
				"notes": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Replaces the note.",
					MaxLength:   1000,
				},
			},
			Required: []string{"person_id"},
		},
	}
}

func (t personUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "person_id"), "person_id")
	if err != nil {
		return nil, err
	}
	before, err := t.svc.GetPerson(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	in := app.UpdatePersonInput{
		WorkspaceID: ws, ID: id,
		Name:  argStringPtr(args, "name"),
		Notes: argStringPtr(args, "notes"),
	}
	if in.Name == nil && in.Notes == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"nothing to change: send a name or notes")
	}
	p, err := t.svc.UpdatePerson(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{"person_id": p.ID.String(), "name": p.Name, "updated": true}
	if in.Name != nil && before.Name != p.Name {
		out["previous_name"] = before.Name
	}
	return out, nil
}

/* ── argument helper shared by the day-of-month fields ───────────────── */

func argIntPtr(args map[string]any, key string) *int {
	v := argInt64Ptr(args, key)
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

/* ── shaping a commitment ────────────────────────────────────────────── */

func recurringRow(f *domain.RecurringEntry, categories map[uuid.UUID]domain.Category, now time.Time) map[string]any {
	m := map[string]any{
		"id":          f.ID.String(),
		"description": f.Description,
		"due_day":     f.DueDay,
		"recurrence":  string(f.Recurrence),
		"status":      string(f.Status),
		"category_id": f.CategoryID.String(),
		// Whether it is actually running right now — the difference between
		// "I have it recorded" and "it moves money this month".
		"active_now": f.ActiveAt(now),
	}
	amountFields(m, "amount", f.AmountCents)
	if f.Recurrence == domain.RecurrenceAnnual {
		// Stated for an annual entry because the monthly figure is derived
		// and a reader would otherwise have to divide.
		amountFields(m, "monthly_equivalent", f.MonthlyEquivalentCents())
	}
	if c, ok := categories[f.CategoryID]; ok {
		m["category"] = c.Name
		// The direction, read through the category — never stored on the
		// entry, so it cannot disagree with the category it points at.
		m["direction"] = string(c.Type)
	}
	if f.EndsAt != nil {
		m["ends_on"] = f.EndsAt.UTC().Format(dateLayout)
		m["cancelled"] = true
	}
	return m
}
