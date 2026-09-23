// Package tools exposes Finance as capabilities an agent can be authorized
// to use.
//
// ── Why this package imports the Agents module's ports ─────────────────
// It implements `chat/ports.Tool`, an interface Agents DECLARES and this
// package SATISFIES. That is dependency inversion, and it is the same
// arrangement GitHub, Job Radar and Threads already use. Nothing here can
// observe which agent is calling, which conversation it belongs to, or
// whether the caller is an agent at all; and nothing in Agents can name
// Finance. There is no `if agent.name == "Ledger"` in this repository and
// there must never be one: capability comes from a grant, and a grant is a
// row an operator can see and revoke.
//
// ── Why it calls the application layer and not the repository ──────────
// Because the rules a write must pass live in `app`: the category is
// resolved there and the transaction's `type` is DERIVED from it, the
// person is checked against the workspace there, the domain validates
// there, and the transfer-pair rule is enforced there. A tool holding a
// repository would be a second writer with its own idea of what a
// transaction is, and the first thing it would get wrong is the one thing
// the composite foreign key exists to prevent — an expense filed under an
// income category. There is no SQL in this package and no way to reach any
// from it.
//
// ── Money and dates have their own files, for the same reason ──────────
// money.go: one representation, int64 cents, derived to text in one
// direction. period.go: the model is never asked for a date it was never
// told. Both are the substance of this sprint rather than plumbing, and
// both carry the argument for what they do.
//
// ── Presentation is not decided here ───────────────────────────────────
// Every output below is a flat map of semantic fields: an id, a word, an
// integer of cents, a date, a rendered amount. There is no markdown, no
// card shape, no colour and no component name. Web draws a table from this,
// a text channel draws a line, and neither is in the contract — which is
// what lets Finance be operated from somewhere React does not run.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

// New builds every Finance tool, ready for chat.Deps.Tools.
//
// Returned as the port type rather than as concrete values: the composition
// root's only job is to hand them over, and a caller that could reach a
// concrete method could reach past the contract.
func New(svc *app.Service, loc *time.Location) []chatports.Tool {
	b := base{svc: svc, loc: loc}
	return []chatports.Tool{
		transactionList{b},
		transactionGet{b},
		transactionCreate{b},
		transactionUpdate{b},
		transactionDelete{b},
		categoryList{b},
		categoryCreate{b},
		categoryUpdate{b},
		cardList{b},
		cardUpdate{b},
		personList{b},
		personCreate{b},
		personUpdate{b},
		recurringList{b},
		recurringCreate{b},
		recurringUpdate{b},
		// One month of the recurrences: what it owes and what is settled.
		// The only source of paid state — see occurrence_tools.go.
		recurringMonth{b},
		markPaid{b},
		markPending{b},
		setMonthAmount{b},
		summaryGet{b},
		recurringSummaryGet{b},
		importSourceList{b},
		importSourceCreate{b},
		importPrepare{b},
		importResolveGroup{b},
		importCommit{b},
	}
}

type base struct {
	svc *app.Service
	loc *time.Location
}

// clock builds this call's period resolver, reading the current instant
// from the application service — which reads it from Postgres.
//
// ── Why every call pays for a round trip to ask the time ───────────────
// Because the alternative is two clocks. The realized/projected split is
// computed by SQL against the database's `now()`; a tool that stamped a
// transaction from this process's clock would be a second authority, and
// the two disagreeing by a few hundred milliseconds is enough to file a
// payment made a moment ago as a future one. Every tool here already makes
// one to three queries; one more, once, is not the cost worth optimising
// against a class of silently wrong totals.
//
// A failure to read it is a failure of the call. There is no fallback to
// time.Now, deliberately: a fallback would restore the second clock exactly
// when the system is least healthy, and it would do so silently.
func (b base) clock(ctx context.Context) (resolver, error) {
	now, err := b.svc.Now(ctx)
	if err != nil {
		return resolver{}, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"the current date could not be read, so no period could be resolved")
	}
	return resolver{loc: b.loc, now: func() time.Time { return now }}, nil
}

/* ── the tool names ──────────────────────────────────────────────────── */

// The seven capabilities, named once.
//
// Exported and constant because more than one thing depends on each string
// being exactly this: the tool's own Definition, the hydration policy in
// references.go, the composition root's grant fixtures and the tests. Two
// literals would be a rename that goes half-done — and a half-done rename
// of TransactionGetTool turns hydration permanently unauthorized without
// failing a build.
const (
	TransactionListTool   chatdomain.ToolName = "finance.transaction.list"
	TransactionGetTool    chatdomain.ToolName = "finance.transaction.get"
	TransactionCreateTool chatdomain.ToolName = "finance.transaction.create"
	TransactionUpdateTool chatdomain.ToolName = "finance.transaction.update"
	TransactionDeleteTool chatdomain.ToolName = "finance.transaction.delete"
	CategoryListTool      chatdomain.ToolName = "finance.category.list"
	SummaryGetTool        chatdomain.ToolName = "finance.summary.get"
)

/* ── the workspace ───────────────────────────────────────────────────── */

// workspaceOf reads the workspace this call belongs to.
//
// The MODEL never supplies a workspace and there is no argument through
// which it could. A missing one is a refusal, never a default: guessing
// would mean reading — or writing — another person's money, and there is no
// fallback value that is not somebody's real ledger.
func workspaceOf(ctx context.Context) (uuid.UUID, error) {
	ws, ok := workspace.FromContext(ctx)
	if !ok || ws == uuid.Nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this call arrived without a workspace, so no Finance data could be resolved")
	}
	return ws, nil
}

/* ── error translation ───────────────────────────────────────────────── */

// toolError turns a domain error into one the model can act on.
//
// The mapping is the whole reason the domain has a Kind. A missing
// transaction must reach the model as something it can respond to by
// listing and retrying, not as `internal_error`, which teaches it only that
// the tool is broken. A CONFLICT — which today means "this row is one leg
// of a transfer, delete the pair instead" — is likewise a fact about the
// request, carrying the application's own sentence, so the model explains
// the real situation rather than inventing a reason.
func toolError(err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, err.Error())
	}
	switch de.Kind {
	case domain.KindInvalid, domain.KindNotFound, domain.KindConflict:
		return chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, de.Message)
	default:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, de.Message)
	}
}

/* ── shared shaping ──────────────────────────────────────────────────── */

// maxToolResultBytes bounds what these tools hand back.
//
// Below the Agents ceiling on purpose: Agents refuses an oversized result
// as an EXECUTION FAILURE, which the model reads as "the tool is broken"
// and answers by apologising. A tool that bounds itself returns a smaller
// honest answer instead.
const maxToolResultBytes = 24 << 10

// fit shrinks a listing until it fits, and says so when it had to.
//
// A truncated list that does not declare itself is indistinguishable from a
// complete one, and a model that summed it would report a total that is
// simply wrong — which, in this module, is the worst thing that can happen.
func fit(out map[string]any, listKey string) (chatdomain.ToolOutput, error) {
	if size(out) <= maxToolResultBytes {
		return out, nil
	}
	items, _ := out[listKey].([]map[string]any)
	if listKey == "" || items == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"the result was larger than one tool call may carry; ask for something narrower")
	}
	kept := len(items)
	for kept > 0 {
		kept /= 2
		out[listKey] = items[:kept]
		out["truncated"] = true
		out["truncated_note"] = "the full result did not fit in one tool call, so it is INCOMPLETE. " +
			"Do NOT add these amounts up and present the result as a total — use finance.summary.get " +
			"for any figure that has to be correct, and narrow this request to inspect individual rows."
		if size(out) <= maxToolResultBytes {
			return out, nil
		}
	}
	return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
		"even a single result was larger than one tool call may carry")
}

func size(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return maxToolResultBytes + 1
	}
	return len(raw)
}

/* ── shared arguments ────────────────────────────────────────────────── */

// transactionIDProperty is the argument the three id-taking tools share.
//
// The model is told where the id comes from, because the alternative is a
// model that invents a plausible uuid. Saying it must come from a listing
// turns "I do not have one" into "call list first", which is a recoverable
// state, rather than into a fabricated argument that resolves to nothing —
// or, worse, to something.
var transactionIDProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "The transaction's id, exactly as returned by finance.transaction.list, " +
		"finance.summary.get or finance.transaction.create. Never invent one: if you do " +
		"not have an id, list first.",
	MaxLength: 36,
}

func parseID(raw, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			field+" is not a valid id; use an id returned by a Finance capability, never one you composed")
	}
	return id, nil
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// argStringPtr distinguishes an argument that was SENT from one that was
// not, which is the whole grammar of `update`.
func argStringPtr(args map[string]any, key string) *string {
	raw, present := args[key]
	if !present {
		return nil
	}
	v, ok := raw.(string)
	if !ok {
		return nil
	}
	return &v
}

func argInt(args map[string]any, key string, fallback int) int {
	switch n := args[key].(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return fallback
	}
}

func argInt64Ptr(args map[string]any, key string) *int64 {
	raw, present := args[key]
	if !present {
		return nil
	}
	switch n := raw.(type) {
	case int64:
		return &n
	case float64:
		v := int64(n)
		return &v
	default:
		return nil
	}
}

/* ── the vocabularies, read from the domain ──────────────────────────── */

// The three enums a caller may name are described from the domain's own
// parsers, never from a literal list written here: a sixth payment method
// would not update a hand-written string, and the model would be told a
// contract the validator does not hold.

func parseEntryType(raw string) (domain.EntryType, error) {
	t := domain.EntryType(strings.ToLower(strings.TrimSpace(raw)))
	if !t.Valid() {
		return "", chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"type must be one of: "+strings.Join(entryTypeNames(), ", "))
	}
	return t, nil
}

func entryTypeNames() []string {
	return []string{string(domain.EntryTypeIncome), string(domain.EntryTypeExpense)}
}

func parseStatus(raw string) (domain.TransactionStatus, error) {
	s := domain.TransactionStatus(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"status must be one of: "+strings.Join(statusNames(), ", "))
	}
	return s, nil
}

func statusNames() []string {
	return []string{
		string(domain.TransactionStatusPaid),
		string(domain.TransactionStatusPending),
		string(domain.TransactionStatusScheduled),
	}
}

func parsePaymentMethod(raw string) (domain.PaymentMethod, error) {
	m := domain.PaymentMethod(strings.ToLower(strings.TrimSpace(raw)))
	if !m.Valid() {
		return "", chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"payment_method must be one of: "+strings.Join(paymentMethodNames(), ", "))
	}
	// ── Why `transfer` is refused as an argument ────────────────────────
	// Because in this domain a transfer is not a payment method, it is a
	// PAIR of rows sharing a transfer_pair_id, created atomically by an
	// application operation these tools do not expose. A single row wearing
	// `payment_method = transfer` would look like a transfer to a person
	// reading the table and would be counted as an ordinary expense by every
	// aggregation — which is exactly the mistake the platform's own rule
	// names: a transfer is detected by transfer_pair_id, never by
	// payment_method.
	if m == domain.PaymentMethodTransfer {
		return "", chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"'transfer' is not a payment method here: a transfer between the user's own "+
				"accounts is a linked pair of rows, and creating one is not among your "+
				"capabilities. If money moved between the user's own accounts, say so and "+
				"leave it to be recorded in the Finance screens; if this was a payment TO "+
				"someone, use pix, debit, credit or cash.")
	}
	return m, nil
}

func paymentMethodNames() []string {
	// `transfer` is deliberately absent — see parsePaymentMethod.
	return []string{
		string(domain.PaymentMethodPix),
		string(domain.PaymentMethodDebit),
		string(domain.PaymentMethodCredit),
		string(domain.PaymentMethodCash),
	}
}

/* ── row shaping ─────────────────────────────────────────────────────── */

// summaryRow is one transaction as a listing entry.
//
// Category is reported by NAME as well as by id: the id is what every other
// capability takes, and the name is the only part a person recognises. The
// caller resolves names once per call, so a listing of forty rows costs one
// extra query and not forty.
func summaryRow(t *domain.Transaction, categories map[uuid.UUID]domain.Category) map[string]any {
	m := map[string]any{
		"id":          t.ID.String(),
		"type":        string(t.Type),
		"description": t.Description,
		"occurred_on": t.OccurredAt.In(time.UTC).Format(dateLayout),
		"status":      string(t.Status),
		"category_id": t.CategoryID.String(),
	}
	amountFields(m, "amount", t.AmountCents)
	if c, ok := categories[t.CategoryID]; ok {
		m["category"] = c.Name
	}
	// The two composite structures a single row can belong to. Both are
	// stated rather than hidden, because a model that edits one leg of a
	// transfer, or deletes installment 3 of 12, has broken something the
	// user cannot see broken.
	if t.TransferPairID != nil {
		m["transfer_leg"] = true
	}
	if t.PlanID != nil && t.InstallmentNumber != nil {
		m["installment_number"] = *t.InstallmentNumber
		m["plan_id"] = t.PlanID.String()
	}
	return m
}

// detailRow is one transaction in full, for `get`.
func detailRow(t *domain.Transaction, categories map[uuid.UUID]domain.Category) map[string]any {
	m := summaryRow(t, categories)
	m["payment_method"] = string(t.PaymentMethod)
	m["source"] = string(t.Source)
	m["notes"] = t.Notes
	// The exact instant, alongside the calendar date, for the one case the
	// date cannot answer: two purchases on the same day, told apart by when.
	m["occurred_at"] = t.OccurredAt.UTC().Format(time.RFC3339)
	m["created_at"] = t.CreatedAt.UTC().Format(time.RFC3339)
	m["updated_at"] = t.UpdatedAt.UTC().Format(time.RFC3339)
	return m
}

// categoryIndex reads the workspace's categories once, so rows can be
// named.
//
// A failure to read them is NOT a failure of the call: the ids are still
// correct and the amounts are still correct, and refusing the whole answer
// because the labels were unavailable would be trading a complete answer
// for none. The rows simply come back without a `category` field.
func (b base) categoryIndex(ctx context.Context, ws uuid.UUID) map[uuid.UUID]domain.Category {
	cats, err := b.svc.ListCategories(ctx, app.ListCategoriesInput{WorkspaceID: ws, Limit: 200})
	if err != nil {
		return nil
	}
	out := make(map[uuid.UUID]domain.Category, len(cats))
	for _, c := range cats {
		out[c.ID] = c
	}
	return out
}

// windowFrom reads the period arguments shared by the two windowed reads.
//
// The precedence is explicit: `from`+`to` win when both are present,
// because a caller that named exact dates meant them; otherwise the period
// word; otherwise the default. A half-specified custom range (one bound
// only) is refused rather than silently completed, since the completion
// would be this package inventing half of a window the user gave the other
// half of.
func (b base) windowFrom(clk resolver, args map[string]any, fallback Period) (Window, error) {
	from := strings.TrimSpace(argString(args, "from"))
	to := strings.TrimSpace(argString(args, "to"))
	if from != "" || to != "" {
		if from == "" || to == "" {
			return Window{}, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
				"from and to must be sent together, both as YYYY-MM-DD, or neither — send a `period` instead")
		}
		w, ok := clk.customWindow(from, to)
		if !ok {
			return Window{}, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
				"from and to must be calendar dates as YYYY-MM-DD, with from on or before to")
		}
		return w, nil
	}
	p := Period(strings.ToLower(strings.TrimSpace(argString(args, "period"))))
	if p == "" {
		p = fallback
	}
	w, ok := clk.resolvePeriod(p)
	if !ok {
		return Window{}, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"period must be one of: "+strings.Join(periodNames(), ", ")+
				" — or send from and to as calendar dates")
	}
	return w, nil
}

// windowFields state which window was used, and what today is.
//
// Both, always, on every windowed read. The window because a model that
// does not know which days it is reporting on will describe them wrongly in
// prose while the numbers are right — and the user believes the prose. And
// today's date because nothing else in a turn supplies it: this is where
// the model learns what "hoje" means, and it learns it from the same clock
// that cut the window.
func (b base) windowFields(clk resolver, out map[string]any, w Window) {
	out["window"] = map[string]any{
		"period":     w.Label,
		"from":       w.From.Format(dateLayout),
		"to":         w.To.AddDate(0, 0, -1).Format(dateLayout), // inclusive last day
		"days":       w.Days(),
		"time_zone":  clk.loc.String(),
		"is_partial": w.To.After(clk.now()),
	}
	out["today"] = clk.today().Format(dateLayout)
}

/* ── finance.transaction.list ────────────────────────────────────────── */

type transactionList struct{ base }

func (transactionList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   TransactionListTool,
		Title:  "Finance · Listar transações",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Lists individual financial transactions in a period, with each one's " +
			"amount, date, description, category and status. Use it to INSPECT specific " +
			"records: to find the transaction the user is talking about before correcting " +
			"or removing it, to answer \"o que eu gastei na semana passada\", to check " +
			"whether something was already recorded, or to find the largest expenses with " +
			"order='largest'. " +
			"Do NOT use it to compute totals: it returns a bounded page, so adding up what " +
			"comes back would give a number that is silently wrong whenever there is more. " +
			"finance.summary.get aggregates server-side and is the only correct source for " +
			"any figure. " +
			"If more than one row plausibly matches what the user meant, ask which one " +
			"before acting on it.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"period": {
					Type:        chatdomain.TypeString,
					Description: periodDescription("Optional. Which period to list. Defaults to last_30_days."),
					MaxLength:   20,
				},
				"from": {
					Type: chatdomain.TypeString,
					Description: "Optional. First day to include, as YYYY-MM-DD. Only for a range no " +
						"`period` expresses; must be sent together with `to`.",
					MaxLength: 10,
				},
				"to": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Last day to include, as YYYY-MM-DD, INCLUSIVE. Sent with `from`.",
					MaxLength:   10,
				},
				"type": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only income, or only expense. One of: " + strings.Join(entryTypeNames(), ", ") + ".",
					MaxLength:   10,
				},
				"status": {
					Type: chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(statusNames(), ", ") +
						". 'paid' is money that moved; 'scheduled' and 'pending' have not moved yet.",
					MaxLength: 12,
				},
				"category_id": {
					Type: chatdomain.TypeString,
					Description: "Optional. Only this category. Get the id from finance.category.list — " +
						"never write a category name here.",
					MaxLength: 36,
				},
				"search": {
					Type: chatdomain.TypeString,
					Description: "Optional. Matches the description, case-insensitively, anywhere in it. " +
						"This is how you find \"aquela compra da Amazon\".",
					MaxLength: 120,
				},
				"min_amount_cents": {
					Type: chatdomain.TypeInteger,
					Description: "Optional. Only rows of at least this amount, in CENTS (R$ 100,00 is 10000). " +
						"Use a range around the figure to find a transaction the user named by value.",
				},
				"max_amount_cents": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional. Only rows of at most this amount, in CENTS.",
				},
				"order": {
					Type: chatdomain.TypeString,
					Description: "Optional. 'recent' (default) returns the most recent first; 'largest' " +
						"returns the biggest amounts first, which is how you answer \"minhas maiores despesas\".",
					MaxLength: 10,
				},
				"limit": {
					Type:        chatdomain.TypeInteger,
					Description: "How many rows to return, 1 to 50. Defaults to 20.",
				},
			},
		},
	}
}

func (t transactionList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	clk, err := t.clock(ctx)
	if err != nil {
		return nil, err
	}
	w, err := t.windowFrom(clk, args, PeriodLast30Days)
	if err != nil {
		return nil, err
	}

	limit := argInt(args, "limit", 20)
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}

	in := app.ListTransactionsInput{
		WorkspaceID:    ws,
		From:           &w.From,
		To:             &w.To,
		Search:         argString(args, "search"),
		MinAmountCents: argInt64Ptr(args, "min_amount_cents"),
		MaxAmountCents: argInt64Ptr(args, "max_amount_cents"),
		Limit:          limit,
	}
	if raw := strings.TrimSpace(argString(args, "type")); raw != "" {
		ty, err := parseEntryType(raw)
		if err != nil {
			return nil, err
		}
		in.Type = &ty
	}
	if raw := strings.TrimSpace(argString(args, "status")); raw != "" {
		st, err := parseStatus(raw)
		if err != nil {
			return nil, err
		}
		in.Status = &st
	}
	if raw := strings.TrimSpace(argString(args, "category_id")); raw != "" {
		id, err := parseID(raw, "category_id")
		if err != nil {
			return nil, err
		}
		in.CategoryID = &id
	}
	switch strings.ToLower(strings.TrimSpace(argString(args, "order"))) {
	case "", "recent":
		in.Order = ports.OrderRecent
	case "largest":
		in.Order = ports.OrderLargest
	default:
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"order must be 'recent' or 'largest'")
	}

	res, err := t.svc.ListTransactions(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}

	cats := t.categoryIndex(ctx, ws)
	rows := make([]map[string]any, 0, len(res.Items))
	for i := range res.Items {
		rows = append(rows, summaryRow(&res.Items[i], cats))
	}

	out := map[string]any{"transactions": rows}
	t.windowFields(clk, out, w)
	// Said explicitly rather than left to be inferred from the row count. A
	// model that concludes "that is all of them" because it asked for 20 and
	// got 20 will state a total that is missing everything after row 20.
	out["has_more"] = res.NextCursor != nil || len(rows) == limit
	if len(rows) == 0 {
		out["note"] = "no transaction matched. The period, the filters, or both may be too narrow — " +
			"this does NOT mean the user has no records."
	}
	return fit(out, "transactions")
}

/* ── finance.transaction.get ─────────────────────────────────────────── */

type transactionGet struct{ base }

func (transactionGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   TransactionGetTool,
		Title:  "Finance · Ver transação",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Reads one transaction in full: amount, date, description, category, " +
			"status, payment method, origin and notes. Call it before changing or removing " +
			"anything, so the change is made against what is stored NOW rather than against " +
			"what was said earlier in the conversation — the record may have moved since. " +
			"Use finance.transaction.list to find the id.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"transaction_id": transactionIDProperty,
			},
			Required: []string{"transaction_id"},
		},
	}
}

func (t transactionGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "transaction_id"), "transaction_id")
	if err != nil {
		return nil, err
	}
	tx, err := t.svc.GetTransaction(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{"transaction": detailRow(tx, t.categoryIndex(ctx, ws))}
	if clk, err := t.clock(ctx); err == nil {
		// Best-effort here, and only here: the record has been read and is
		// correct, so refusing the whole answer because the date could not
		// be stated as well would trade a complete result for none.
		out["today"] = clk.today().Format(dateLayout)
	}
	return fit(out, "")
}

/* ── finance.transaction.create ──────────────────────────────────────── */

type transactionCreate struct{ base }

func (transactionCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   TransactionCreateTool,
		Title:  "Finance · Registrar transação",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		// ── The distinctions this description exists to hold ────────────
		// Ordinary speech about money is mostly NOT about money that moved.
		// "Se eu gastar 3 mil por mês", "estou pensando em comprar um
		// notebook", "um Uber custa uns 30 reais" are a simulation, an
		// intention and a general fact; every one of them contains a number
		// and a thing bought, which is the entire shape of a transaction.
		// A model that records them fills the ledger with money that was
		// never spent, and the totals it later reports are wrong in a way
		// the user cannot see without auditing every row.
		//
		// The asymmetry decides the wording. A fact not recorded is a
		// sentence away from being recorded; a fabricated expense corrupts
		// every figure derived from it until someone finds it. So the
		// description names the wrong readings explicitly rather than
		// describing the right one and hoping.
		Description: "Records a financial event that ACTUALLY HAPPENED: money that moved, " +
			"or a payment that is genuinely scheduled. Use it when the user reports one " +
			"— \"paguei 89,90 no mercado\", \"recebi o salário\", \"gastei 58 de Uber\" — " +
			"or asks for one to be added. " +
			"NEVER record a hypothesis, a simulation, a plan, an intention, an estimate or " +
			"a general fact about prices. \"Se eu gastar X\", \"estou pensando em comprar\", " +
			"\"queria gastar menos\", \"um Uber custa uns 30\" and \"esse mês vai ser caro\" " +
			"describe money that has not moved, and recording any of them puts an expense " +
			"in the ledger that never happened. When in doubt about whether an event was " +
			"real, ask — a question costs a sentence, a fabricated transaction corrupts " +
			"every total until someone finds it. " +
			"Every transaction needs a category, and its income/expense direction comes " +
			"from that category, so call finance.category.list first and pick the one that " +
			"fits; do not guess an id. " +
			"Before recording a second time in one conversation, check whether you already " +
			"recorded it: an acknowledgement like \"boa\" is not a second event, and a " +
			"correction of what you just saved is finance.transaction.update.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"category_id": {
					Type: chatdomain.TypeString,
					Description: "Required. The category, by id, from finance.category.list. It also " +
						"decides whether this is income or an expense — there is no separate field, " +
						"so picking an income category for a purchase records the purchase as money " +
						"received.",
					MaxLength: 36,
				},
				"amount_cents": {
					Type: chatdomain.TypeInteger,
					Description: "Required. The amount in CENTS, as a whole number, always positive. " +
						"R$ 89,90 is 8990. R$ 90 is 9000. R$ 1.250,00 is 125000. R$ 0,99 is 99. " +
						"Multiply by 100 and never send a decimal: 89.90 is rejected, and sending " +
						"89 would record eighty-nine centavos.",
				},
				"description": {
					Type: chatdomain.TypeString,
					Description: "Required. What this was, in the user's own words, short: \"mercado\", " +
						"\"Uber para o aeroporto\", \"salário\". This is how the transaction is " +
						"recognised later, including by you when the user asks to correct it.",
					MaxLength: 280,
				},
				"occurred_on": {
					Type: chatdomain.TypeString,
					Description: "Optional, YYYY-MM-DD. LEAVE IT OUT when the event is happening now or " +
						"the user said \"hoje\" — the current date is then taken from the server, which " +
						"is more reliable than a date you compose. " +
						"Send it only for a day the user actually named, including a relative one like " +
						"\"ontem\" or \"sexta passada\", and work that day out by counting from the " +
						"`today` field that finance.category.list returns — the same call you already " +
						"make to choose a category. Every Finance read carries `today` for the same " +
						"reason. NEVER compose this date from your own sense of the current date: you " +
						"do not have one, and a date invented here files real money in the wrong week.",
					MaxLength: 10,
				},
				"status": {
					Type: chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(statusNames(), ", ") +
						". Defaults to 'paid', which is right for something that already happened. " +
						"Use 'scheduled' only for a payment with a future date the user described as " +
						"upcoming, and 'pending' for one that is due and not yet settled.",
					MaxLength: 12,
				},
				"payment_method": {
					Type: chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(paymentMethodNames(), ", ") +
						". Send it only when the user said how they paid; otherwise leave it out.",
					MaxLength: 12,
				},
				"notes": {
					Type: chatdomain.TypeString,
					Description: "Optional. Context the description has no room for. Do not invent " +
						"detail the user did not give.",
					MaxLength: 1000,
				},
			},
			Required: []string{"category_id", "amount_cents", "description"},
		},
	}
}

func (t transactionCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	categoryID, err := parseID(argString(args, "category_id"), "category_id")
	if err != nil {
		return nil, err
	}
	amount := argInt64Ptr(args, "amount_cents")
	if amount == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"amount_cents is required and must be a whole number of cents")
	}
	clk, err := t.clock(ctx)
	if err != nil {
		return nil, err
	}
	occurredAt, ok := clk.occurredAt(argString(args, "occurred_on"))
	if !ok {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"occurred_on must be a calendar date as YYYY-MM-DD, or left out for today")
	}

	// ── Why `source` is set here and is not an argument ─────────────────
	// The domain already models where a row came from, and `ai` is one of
	// its four values. Every row these tools write IS agent-written, so the
	// provenance is a fact about the writer rather than a choice for the
	// model — offering it as an argument would let a conversation file its
	// own writes as `manual` and make the origin badge lie.
	source := domain.TransactionSourceAI
	in := app.CreateTransactionInput{
		WorkspaceID: ws,
		CategoryID:  categoryID,
		AmountCents: *amount,
		Description: argString(args, "description"),
		OccurredAt:  occurredAt,
		Source:      &source,
	}
	if raw := strings.TrimSpace(argString(args, "status")); raw != "" {
		st, err := parseStatus(raw)
		if err != nil {
			return nil, err
		}
		in.Status = &st
	}
	if raw := strings.TrimSpace(argString(args, "payment_method")); raw != "" {
		m, err := parsePaymentMethod(raw)
		if err != nil {
			return nil, err
		}
		in.PaymentMethod = &m
	}
	if n := argStringPtr(args, "notes"); n != nil {
		in.Notes = n
	}

	tx, err := t.svc.CreateTransaction(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}

	// The stored values, read back from what the service returned rather
	// than echoed from the arguments. The amount comes back BOTH ways — see
	// money.go — so the model's confirmation to the user quotes the figure
	// that is actually in the database, and an amount that was scaled wrongly
	// becomes visible in the same sentence that reports success.
	out := map[string]any{
		"transaction_id": tx.ID.String(),
		"type":           string(tx.Type),
		"description":    tx.Description,
		"occurred_on":    tx.OccurredAt.In(t.loc).Format(dateLayout),
		"status":         string(tx.Status),
		"created":        true,
	}
	amountFields(out, "amount", tx.AmountCents)
	if cat, err := t.svc.GetCategory(ctx, ws, tx.CategoryID); err == nil {
		out["category"] = cat.Name
	}
	return out, nil
}

/* ── finance.transaction.update ──────────────────────────────────────── */

type transactionUpdate struct{ base }

func (transactionUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   TransactionUpdateTool,
		Title:  "Finance · Corrigir transação",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Corrects one transaction that is already recorded: its amount, date, " +
			"description, category or status. Use it when the user revises something they " +
			"told you — \"na verdade eram 79,90\", \"isso foi ontem\", \"essa é da categoria " +
			"errada\", \"já paguei aquela conta\". Send only the fields that change; " +
			"everything you leave out is kept exactly as it is. " +
			"A correction REPLACES a value, so make sure you are correcting the right row: " +
			"find it with finance.transaction.list and, if more than one could be the one " +
			"they mean, ask before changing anything. Changing the wrong record is not " +
			"something the user can see happening. " +
			"Correcting an existing record is not the same as recording a new one: when the " +
			"user revises what they just told you, update that row rather than creating a " +
			"second transaction.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"transaction_id": transactionIDProperty,
				"amount_cents": {
					Type: chatdomain.TypeInteger,
					Description: "Optional. The corrected amount in CENTS, whole number, positive. " +
						"R$ 79,90 is 7990. Never a decimal.",
				},
				"description": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The corrected description.",
					MaxLength:   280,
				},
				"occurred_on": {
					Type:        chatdomain.TypeString,
					Description: "Optional, YYYY-MM-DD. The corrected date.",
					MaxLength:   10,
				},
				"category_id": {
					Type: chatdomain.TypeString,
					Description: "Optional. A different category, by id from finance.category.list. " +
						"Moving a row to a category of the other direction also flips it between " +
						"income and expense, so only do it when that is what the user meant.",
					MaxLength: 36,
				},
				"status": {
					Type: chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(statusNames(), ", ") +
						". Use 'paid' when a scheduled payment has now been made.",
					MaxLength: 12,
				},
				"payment_method": {
					Type:        chatdomain.TypeString,
					Description: "Optional. One of: " + strings.Join(paymentMethodNames(), ", ") + ".",
					MaxLength:   12,
				},
				"notes": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Replaces the notes.",
					MaxLength:   1000,
				},
			},
			Required: []string{"transaction_id"},
		},
	}
}

func (t transactionUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "transaction_id"), "transaction_id")
	if err != nil {
		return nil, err
	}

	// Read first, for three reasons: the result can report what actually
	// moved, an id from another workspace fails here exactly as it fails in
	// the update itself, and the row's structural role has to be known
	// before an amount or a date is touched.
	current, err := t.svc.GetTransaction(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	amount := argInt64Ptr(args, "amount_cents")
	occurredRaw := argStringPtr(args, "occurred_on")

	// ── Why a transfer leg refuses exactly these two fields ─────────────
	// A transfer is two rows that must agree: same amount, same date,
	// opposite direction. Nothing in the schema enforces that agreement —
	// the application maintains it when the pair is created and there is no
	// operation to re-establish it afterwards. Changing one leg's amount
	// therefore produces a pair that is silently unbalanced, and the
	// dashboard, which excludes both legs from its totals, will never show
	// it. The rest of the row — its description, its notes — carries no
	// such invariant and stays editable.
	//
	// This is a REFUSAL, not a different operation: the update below is the
	// same application call any surface makes.
	if current.TransferPairID != nil && (amount != nil || occurredRaw != nil) {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"this row is one leg of a transfer between the user's own accounts, and the two "+
				"legs must keep the same amount and date. Changing one of them here would "+
				"leave the pair inconsistent. Tell the user this has to be corrected in the "+
				"Finance screens; you can still fix its description or notes.")
	}

	in := app.UpdateTransactionInput{
		WorkspaceID: ws,
		ID:          id,
		AmountCents: amount,
		Description: argStringPtr(args, "description"),
		Notes:       argStringPtr(args, "notes"),
	}
	if occurredRaw != nil {
		clk, err := t.clock(ctx)
		if err != nil {
			return nil, err
		}
		when, ok := clk.occurredAt(*occurredRaw)
		if !ok || strings.TrimSpace(*occurredRaw) == "" {
			return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
				"occurred_on must be a calendar date as YYYY-MM-DD")
		}
		in.OccurredAt = &when
	}
	if raw := argStringPtr(args, "category_id"); raw != nil {
		catID, err := parseID(*raw, "category_id")
		if err != nil {
			return nil, err
		}
		in.CategoryID = &catID
	}
	if raw := argStringPtr(args, "status"); raw != nil {
		st, err := parseStatus(*raw)
		if err != nil {
			return nil, err
		}
		in.Status = &st
	}
	if raw := argStringPtr(args, "payment_method"); raw != nil {
		m, err := parsePaymentMethod(*raw)
		if err != nil {
			return nil, err
		}
		in.PaymentMethod = &m
	}

	updated, err := t.svc.UpdateTransaction(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"transaction_id": updated.ID.String(),
		"type":           string(updated.Type),
		"description":    updated.Description,
		"occurred_on":    updated.OccurredAt.In(t.loc).Format(dateLayout),
		"status":         string(updated.Status),
		"updated":        true,
	}
	amountFields(out, "amount", updated.AmountCents)
	// What the amount WAS, when it changed. A confirmation that says "de R$
	// 89,90 para R$ 79,90" is checkable by the person reading it; one that
	// says only the new figure is an assertion.
	if amount != nil && current.AmountCents != updated.AmountCents {
		amountFields(out, "previous_amount", current.AmountCents)
	}
	if cat, err := t.svc.GetCategory(ctx, ws, updated.CategoryID); err == nil {
		out["category"] = cat.Name
	}
	if updated.PlanID != nil {
		out["note"] = "this row is one installment of a purchase plan; the plan's own totals " +
			"are maintained in the Finance screens and are not affected by this change."
	}
	return out, nil
}

/* ── finance.transaction.delete ──────────────────────────────────────── */

type transactionDelete struct{ base }

func (transactionDelete) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   TransactionDeleteTool,
		Title:  "Finance · Remover transação",
		Effect: chatdomain.EffectWrite,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		// ── The distinction this description exists to hold ─────────────
		// Judging a purchase and removing its record are different acts,
		// and ordinary language conflates them: "essa compra foi péssima",
		// "me arrependi disso", "não devia ter gasto isso" all sound like
		// verdicts on whether the row should exist. They are not. They are
		// feedback about a decision that was already made, and the record of
		// it is exactly what makes the feedback true.
		//
		// The asymmetry decides it. Removing a real expense makes every
		// total silently wrong and the user has no way to notice; leaving a
		// regretted purchase in place costs nothing at all.
		Description: "Removes a transaction from the record. Use it ONLY when the user " +
			"explicitly asks for the RECORD to be removed — \"apaga essa transação\", " +
			"\"isso foi cadastrado errado\", \"era teste, pode excluir\", \"lancei duas " +
			"vezes\". " +
			"This is NOT how regret or criticism is handled. \"Essa compra foi péssima\", " +
			"\"me arrependi\", \"gastei demais nisso\" and \"não devia ter comprado\" are " +
			"judgements about a purchase that really happened: the record stays, because " +
			"deleting it would make every total wrong while pretending the money came back. " +
			"It is also not how a refund or a cancellation is recorded — those are new " +
			"financial events, not the absence of the old one. " +
			"If more than one row could be the one they mean, ask which one before removing " +
			"anything: this changes the user's financial history and they cannot see it " +
			"happening.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"transaction_id": transactionIDProperty,
			},
			Required: []string{"transaction_id"},
		},
	}
}

func (t transactionDelete) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "transaction_id"), "transaction_id")
	if err != nil {
		return nil, err
	}

	// Read before removing, so the result can name what was removed — the
	// model acknowledges "apaguei o lançamento de R$ 89,90 do mercado"
	// instead of echoing a uuid — and so an id belonging to another
	// workspace fails here exactly as it fails in the delete itself.
	tx, err := t.svc.GetTransaction(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	// The same application operation the Finance screens call. It is a SOFT
	// delete: the row stops appearing everywhere and the history is not
	// destroyed. There is no second removal semantics for agents. It is also
	// where the transfer-leg refusal lives, and the refusal reaches the model
	// as the application's own sentence rather than one written here twice.
	if err := t.svc.DeleteTransaction(ctx, ws, id); err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"transaction_id": tx.ID.String(),
		"description":    tx.Description,
		"occurred_on":    tx.OccurredAt.In(t.loc).Format(dateLayout),
		"type":           string(tx.Type),
		"deleted":        true,
	}
	amountFields(out, "amount", tx.AmountCents)
	if tx.PlanID != nil && tx.InstallmentNumber != nil {
		out["note"] = "this was installment number " + itoa(*tx.InstallmentNumber) +
			" of a purchase plan. The plan itself was not removed and its remaining count " +
			"is not recalculated here — tell the user, so they can check it in the Finance screens."
	}
	return out, nil
}

/* ── finance.category.list ───────────────────────────────────────────── */

type categoryList struct{ base }

func (categoryList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   CategoryListTool,
		Title:  "Finance · Listar categorias",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Lists the categories this workspace actually uses, each with its id and " +
			"whether it is an income or an expense category. Call it before recording or " +
			"recategorising anything: a transaction's category is required, and the " +
			"income/expense direction is taken FROM the category rather than from a separate " +
			"field. " +
			"It also returns `today`, the current date read from the Finance clock. That is " +
			"the ONLY date you may reason from: work out \"ontem\", \"anteontem\" or \"sexta " +
			"passada\" by counting from it, never from a date you remember. " +
			"The list is the whole vocabulary that exists — if nothing fits what the user " +
			"described, say so and ask which category they want it under. Creating a new " +
			"category is not among your capabilities, and picking a loosely-related one " +
			"quietly misfiles the record.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"type": {
					Type: chatdomain.TypeString,
					Description: "Optional. Only categories of one direction. One of: " +
						strings.Join(entryTypeNames(), ", ") + ".",
					MaxLength: 10,
				},
			},
		},
	}
}

func (t categoryList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	// ── Why a category listing asks what day it is ──────────────────────
	// Because this is the call that PRECEDES almost every write. The model
	// is told to pick a category before recording anything, so in a turn
	// like "ontem gastei 90 no cinema" this is the only read that happens
	// before finance.transaction.create — and without a date in its result
	// the model reaches the write with no anchor at all. It then either
	// omits occurred_on, filing yesterday's expense under today, or composes
	// a date from its training, which is the failure period.go exists to
	// prevent.
	//
	// The other reads have always carried `today`. That this one did not was
	// a gap rather than a decision: nothing about listing categories made
	// the date less necessary, it simply was not the read anybody was
	// thinking about when the rule was written.
	clk, err := t.clock(ctx)
	if err != nil {
		return nil, err
	}
	in := app.ListCategoriesInput{WorkspaceID: ws, Limit: 200}
	if raw := strings.TrimSpace(argString(args, "type")); raw != "" {
		ty, err := parseEntryType(raw)
		if err != nil {
			return nil, err
		}
		in.Type = &ty
	}
	cats, err := t.svc.ListCategories(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}
	rows := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		// Colour and icon are omitted deliberately: they are how a SCREEN
		// draws a category, they are worth nothing to a model choosing one,
		// and every field here is a prompt token on the next provider call.
		rows = append(rows, map[string]any{
			"id":   c.ID.String(),
			"name": c.Name,
			"type": string(c.Type),
		})
	}
	// `today` is emitted even when the workspace has no category at all: the
	// date is a fact about the clock, not about the list, and a turn that
	// finds no category still needs to know what day the user means.
	out := map[string]any{
		"categories": rows,
		"today":      clk.today().Format(dateLayout),
		"time_zone":  clk.loc.String(),
	}
	if len(rows) == 0 {
		out["note"] = "this workspace has no categories yet, so no transaction can be recorded. " +
			"Tell the user they need to create at least one in the Finance screens."
	}
	return fit(out, "categories")
}

/* ── finance.summary.get ─────────────────────────────────────────────── */

type summaryGet struct{ base }

func (summaryGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   SummaryGetTool,
		Title:  "Finance · Resumo do período",
		Effect: chatdomain.EffectRead,
		// Every Finance capability carries money: an amount, a description of
		// what somebody bought, who it was for. None of that belongs in the
		// audit trail in the clear — see ToolDefinition.Confidential.
		Confidential: true,
		Description: "Aggregates a period server-side and returns what came in, what went " +
			"out, the net, and the breakdown by category and by payment method. This is the " +
			"ONLY correct source for any financial figure: it sums every matching row in the " +
			"database, whereas a listing returns one bounded page. Use it for \"quanto " +
			"gastei este mês\", \"quanto entrou\", \"com o que estou gastando mais\", and " +
			"before any analysis of patterns or capacity to save. " +
			"Amounts are split into REALIZED — money that actually moved — and PROJECTED — " +
			"scheduled or pending, so not yet real. Transfers between the user's own " +
			"accounts are EXCLUDED from both, because moving money between one's own " +
			"accounts is not income or expense. " +
			"Note what this cannot tell you: Finance records flows, not account balances. " +
			"The net of a period is what came in minus what went out in that period, not " +
			"how much money the user has. Do not present it as a balance.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"period": {
					Type:        chatdomain.TypeString,
					Description: periodDescription("Optional. Which period to aggregate. Defaults to this_month."),
					MaxLength:   20,
				},
				"from": {
					Type: chatdomain.TypeString,
					Description: "Optional. First day to include, as YYYY-MM-DD. Only for a range no " +
						"`period` expresses; must be sent with `to`. The window may not exceed 366 days.",
					MaxLength: 10,
				},
				"to": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Last day to include, as YYYY-MM-DD, INCLUSIVE. Sent with `from`.",
					MaxLength:   10,
				},
			},
		},
	}
}

func (t summaryGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	clk, err := t.clock(ctx)
	if err != nil {
		return nil, err
	}
	w, err := t.windowFrom(clk, args, PeriodThisMonth)
	if err != nil {
		return nil, err
	}

	totals, err := t.svc.GetTransactionTotals(ctx, app.GetTransactionTotalsInput{
		WorkspaceID: ws, From: w.From, To: w.To,
	})
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{}
	t.windowFields(clk, out, w)

	realized := totals.ByRealization[ports.RealizationRealized]
	projected := totals.ByRealization[ports.RealizationProjected]
	excluded := totals.ByRealization[ports.RealizationExcluded]

	out["realized"] = bucket(realized)
	out["projected"] = bucket(projected)
	out["excluded_transfers"] = map[string]any{
		"count": excluded.Count,
		"note": "money moved between the user's own accounts. Never income and never " +
			"expense — reported only so the number is not mistaken for something missing.",
	}

	cats := t.categoryIndex(ctx, ws)
	byCategory := make([]map[string]any, 0, len(totals.ByCategory))
	for _, c := range totals.ByCategory {
		row := map[string]any{
			"category_id": c.CategoryID.String(),
			"type":        string(c.Type),
			"count":       c.Count,
		}
		amountFields(row, "total", c.TotalCents)
		if cat, ok := cats[c.CategoryID]; ok {
			row["category"] = cat.Name
		}
		byCategory = append(byCategory, row)
	}
	out["by_category"] = byCategory

	byMethod := make([]map[string]any, 0, len(totals.ByMethod))
	for _, m := range totals.ByMethod {
		row := map[string]any{
			"payment_method": string(m.PaymentMethod),
			"count":          m.Count,
		}
		amountFields(row, "total", m.TotalCents)
		byMethod = append(byMethod, row)
	}
	out["by_payment_method"] = byMethod

	if realized.Count == 0 && projected.Count == 0 {
		out["note"] = "no transaction falls in this window. That is a fact about the period, " +
			"not about whether the user keeps records — try a wider one before concluding anything."
	}
	return fit(out, "by_category")
}

// bucket shapes one realization bucket, with the net computed here rather
// than left to the model.
//
// ── Why the net is computed and not left to arithmetic in the prompt ───
// Because it is a subtraction the model would do in prose, on two large
// integers, in the middle of writing a sentence — and it is the single
// number the user is most likely to act on. Two integers are already here;
// subtracting them costs nothing and removes an entire class of confidently
// wrong answers.
func bucket(b ports.TotalsBucket) map[string]any {
	m := map[string]any{"count": b.Count}
	amountFields(m, "income", b.IncomeCents)
	amountFields(m, "expense", b.ExpenseCents)
	amountFields(m, "net", b.IncomeCents-b.ExpenseCents)
	return m
}

// itoa avoids pulling strconv into a file that is otherwise free of
// formatting; the money and date helpers own their own.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
