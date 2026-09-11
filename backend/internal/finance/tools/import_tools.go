package tools

// Statement import, as capabilities.
//
// ── Why three and not one ──────────────────────────────────────────────
// The operator has to be able to say no in the middle. `prepare` reads a
// statement and says what it thinks; `commit` changes the ledger. A single
// capability that did both would import on the strength of a parse, and a
// parse is precisely the thing nobody has checked yet.
//
// ── Why the statement arrives as one string ────────────────────────────
// A tool schema in this system is a closed subset: a flat object of scalar
// properties, no arrays and no nested objects. A statement is a list, so
// there is no shape in which a model could hand over 150 structured rows.
// It hands over the text; this module parses it. That is also the safer
// arrangement, because it means the numbers that reach the ledger were
// read by a deterministic parser and not retyped by a language model.
//
// ── Why all three are WRITE ────────────────────────────────────────────
// chat/domain.ToolEffect defines read as "running it twice with the same
// input changes nothing and is safe", and write as "the tool changes state
// somewhere". `prepare` creates a batch and its staged rows; running it
// twice creates two batches. That is state, so it is a write, and it
// carries the standard notice telling the model not to run it unless the
// user asked. Declaring it read would have been convenient and false.

import (
	"context"
	"strings"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

const (
	ImportSourceListTool   chatdomain.ToolName = "finance.import_source.list"
	ImportSourceCreateTool chatdomain.ToolName = "finance.import_source.create"
	ImportPrepareTool      chatdomain.ToolName = "finance.import.prepare"
	ImportResolveGroupTool chatdomain.ToolName = "finance.import.resolve_group"
	ImportCommitTool       chatdomain.ToolName = "finance.import.commit"
)

/* ── finance.import.prepare ──────────────────────────────────────────── */

type importPrepare struct{ base }

func (importPrepare) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ImportPrepareTool,
		Title:        "Finance · Analisar extrato",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Reads a bank or card statement pasted as CSV text and says what would " +
			"happen if it were imported. It creates NO transaction: nothing enters the " +
			"ledger, no total changes, and finance.summary.get returns exactly what it " +
			"returned before. " +
			"The text must have a header line naming the columns; date, description and " +
			"amount are required and may be in any order, and the accepted names include " +
			"data, descricao, historico and valor. A negative amount is an expense and a " +
			"positive one is income. An optional id or fitid column, when the bank provides " +
			"one, is what makes duplicate detection certain rather than a guess. " +
			"import_source_id names a registered source from finance.import_source.list. " +
			"Duplicate detection is namespaced by that id, which the database issued: there " +
			"is no argument here through which the account's identity can be spelled, and " +
			"therefore none to spell inconsistently. If the statement's source is not " +
			"registered yet, propose it, get approval, and create it with " +
			"finance.import_source.create. " +
			"The result reports how many lines are ready, how many are already in the " +
			"ledger, how many need a category, how many might be movements between the " +
			"user's own accounts, and how many could not be read. Report those numbers to " +
			"the user and, when categories are missing, propose them and WAIT for approval.",
		Schema: chatdomain.ToolSchema{
			Required: []string{"import_source_id", "content"},
			Properties: map[string]chatdomain.ToolProperty{
				"import_source_id": {
					Type: chatdomain.TypeString,
					Description: "Required. The registered source this statement came from, by id, " +
						"from finance.import_source.list.",
				},
				"source_label": {
					Type: chatdomain.TypeString,
					Description: "Optional. What the user calls THIS import, e.g. \"extrato de agosto\". " +
						"Display only: it is not part of any identity, so it may vary freely.",
				},
				"content": {
					Type:        chatdomain.TypeString,
					Description: "Required. The statement as CSV text, including its header line.",
				},
			},
		},
	}
}

func (t importPrepare) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	sourceID, err := uuid.Parse(strings.TrimSpace(argString(args, "import_source_id")))
	if err != nil {
		return nil, toolError(domain.Invalid(
			"import_source_id must be an id from finance.import_source.list"))
	}
	res, err := t.svc.PrepareImport(ctx, app.PrepareImportInput{
		WorkspaceID:    ws,
		ImportSourceID: sourceID,
		SourceLabel:    argString(args, "source_label"),
		Format:         domain.ImportFormatCSV,
		Content:        argString(args, "content"),
	})
	if err != nil {
		return nil, toolError(err)
	}
	return importSnapshotOutput(res), nil
}

/* ── finance.import.resolve_group ────────────────────────────────────── */

type importResolveGroup struct{ base }

func (importResolveGroup) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ImportResolveGroupTool,
		Title:        "Finance · Classificar grupo do extrato",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Assigns a category to every unclassified line of a prepared statement " +
			"that shares the same description, so a statement of 150 lines is resolved in a " +
			"handful of decisions instead of 150. It still creates NO transaction. " +
			"group_key is the description exactly as finance.import.prepare reported it. " +
			"The category must already exist and must have the same direction as the lines: " +
			"an income category cannot be attached to an expense. If the category the user " +
			"wants does not exist yet, propose it, get approval, create it with " +
			"finance.category.create, and only then call this.",
		Schema: chatdomain.ToolSchema{
			Required: []string{"group_key", "category_id"},
			Properties: map[string]chatdomain.ToolProperty{
				"batch_id": {Type: chatdomain.TypeString, Description: "Optional. Omit it to act on the " +
					"statement most recently prepared in this workspace, which is almost always the right " +
					"one. Never invent an id: omitting is correct, guessing is not."},
				"group_key":   {Type: chatdomain.TypeString, Description: "Required. The description group, as reported by the preview."},
				"category_id": {Type: chatdomain.TypeString, Description: "Required. An existing category, by id, from finance.category.list."},
			},
		},
	}
}

func (t importResolveGroup) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	batchID, err := optionalBatchID(args)
	if err != nil {
		return nil, toolError(err)
	}
	catID, err := uuid.Parse(strings.TrimSpace(argString(args, "category_id")))
	if err != nil {
		return nil, toolError(domain.Invalid("category_id must be an id from finance.category.list"))
	}
	res, err := t.svc.ResolveImportGroup(ctx, app.ResolveImportGroupInput{
		WorkspaceID: ws, BatchID: batchID,
		GroupKey: argString(args, "group_key"), CategoryID: catID,
	})
	if err != nil {
		return nil, toolError(err)
	}
	return importSnapshotOutput(res), nil
}

/* ── finance.import.commit ───────────────────────────────────────────── */

type importCommit struct{ base }

func (importCommit) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ImportCommitTool,
		Title:        "Finance · Importar extrato",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Writes the ready lines of a prepared statement into the ledger, in one " +
			"go. Call it ONLY after the user has explicitly said to import: the preview " +
			"exists so they can decide, and a commit nobody asked for cannot be undone line " +
			"by line. " +
			"It imports only the lines marked ready. Lines that could not be read, lines " +
			"still without a category, lines that might be internal transfers and lines " +
			"that merely resemble something already imported are all left alone and stay " +
			"visible in the reconciliation. " +
			"Re-importing the same statement afterwards creates nothing new for the lines " +
			"the bank identified for us.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"batch_id": {Type: chatdomain.TypeString, Description: "Optional. Omit it to import the " +
					"statement most recently prepared in this workspace. Never invent an id: omitting is " +
					"correct, guessing is not."},
			},
		},
	}
}

func (t importCommit) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	batchID, err := optionalBatchID(args)
	if err != nil {
		return nil, toolError(err)
	}
	res, err := t.svc.CommitImport(ctx, app.CommitImportInput{WorkspaceID: ws, BatchID: batchID})
	if err != nil {
		return nil, toolError(err)
	}
	rec := res.Reconciliation
	return chatdomain.ToolOutput{
		"batch_id":           res.BatchID.String(),
		"created":            res.Created,
		"eligible_ready":     res.EligibleReady,
		"skipped_concurrent": res.SkippedConcurrent,
		"still_staged": map[string]any{
			"unresolved_category": rec.UnresolvedCategory,
			"ambiguous_duplicate": rec.AmbiguousDuplicate,
			"ambiguous_internal":  rec.AmbiguousInternal,
			"invalid":             rec.Invalid,
			"already_imported":    rec.Duplicate,
		},
		"row_count": rec.RowCount,
		"note": "created + skipped_concurrent equals the rows that were ready. Everything else " +
			"is still staged and was not imported.",
	}, nil
}

/* ── shared output ───────────────────────────────────────────────────── */

// importSnapshotOutput reports the arithmetic and a bounded list of
// groups. It never returns the rows themselves: 150 of them would not fit
// MaxToolResultBytes, and a truncated list of a person's spending is worse
// than none because it reads as complete.
func importSnapshotOutput(res *app.PrepareImportResult) chatdomain.ToolOutput {
	rec := res.Reconciliation
	groups := make([]map[string]any, 0, len(res.Groups))
	for _, g := range res.Groups {
		groups = append(groups, map[string]any{
			"group_key": g.GroupKey,
			"rows":      g.Rows,
			"direction": string(g.Direction),
			"example":   g.Sample,
		})
	}
	// The workspace's categories ride along.
	//
	// Not a convenience. Every finance capability is Confidential, so the
	// audit rows the next turn's evidence is read from carry no result, and
	// a model cannot see what it observed a turn ago. It re-derives the
	// whole flow each turn, and a turn allows three tool rounds: preview,
	// resolutions, commit. A separate category listing eats the round the
	// commit needed, which is how a live run looped without ever importing.
	//
	// It carries no decision. Matching a group to a category is still the
	// caller's, and still needs the operator behind it.
	cats := make([]map[string]any, 0, len(res.Categories))
	for _, c := range res.Categories {
		cats = append(cats, map[string]any{
			"id": c.ID.String(), "name": c.Name, "direction": string(c.Type),
		})
	}
	out := chatdomain.ToolOutput{
		"categories":          cats,
		"batch_id":            res.Batch.ID.String(),
		"source_label":        res.Batch.SourceLabel,
		"account_scope":       res.Batch.AccountScope,
		"status":              string(res.Batch.Status),
		"row_count":           rec.RowCount,
		"ready":               rec.Ready,
		"already_imported":    rec.Duplicate,
		"maybe_duplicate":     rec.AmbiguousDuplicate,
		"needs_category":      rec.UnresolvedCategory,
		"maybe_internal":      rec.AmbiguousInternal,
		"invalid":             rec.Invalid,
		"reconciles":          rec.Balances(),
		"nothing_was_written": res.Batch.Status == domain.ImportBatchPrepared,
		"unresolved_groups":   groups,
	}
	if len(groups) > 0 {
		out["next_step"] = "Match each group to one of the categories listed above and call " +
			"finance.import.resolve_group for each, in the SAME turn. Do not call " +
			"finance.category.list first: the categories are already here, and spending a tool " +
			"round on it can leave no round for the import. If a needed category is missing, " +
			"propose it, wait for approval, then create it with finance.category.create."
	}
	if rec.AmbiguousDuplicate > 0 {
		out["maybe_duplicate_note"] = "These match an imported line by date, amount and description " +
			"only. That cannot tell one purchase exported twice from two identical purchases, so " +
			"they were NOT imported and NOT discarded. Ask the user."
	}
	if rec.AmbiguousInternal > 0 {
		out["maybe_internal_note"] = "These look like movements between the user's own accounts. A " +
			"statement shows only one side, so importing them as ordinary income or expense would " +
			"inflate both. Ask the user."
	}
	return out
}

// optionalBatchID reads batch_id when the model supplied one and returns
// the zero uuid when it did not, which the application layer reads as "the
// statement we were just talking about".
//
// A supplied value that is not a uuid is still an error rather than a
// silent fallback: the model believing it has an id and being wrong is a
// different situation from it knowing it has none, and quietly importing
// something else on the strength of a typo is exactly the failure this
// whole flow exists to avoid.
func optionalBatchID(args map[string]any) (uuid.UUID, error) {
	raw := strings.TrimSpace(argString(args, "batch_id"))
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.Invalid("batch_id is not a valid id; omit it to use the statement " +
			"most recently prepared in this workspace")
	}
	return id, nil
}

/* ── finance.import_source.list ──────────────────────────────────────── */

type importSourceList struct{ base }

func (importSourceList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ImportSourceListTool,
		Title:        "Finance · Listar fontes de importação",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Lists the accounts and cards whose statements can be imported, with the id " +
			"finance.import.prepare needs. Duplicate detection is namespaced by that id, so a " +
			"statement must always be imported under the same source it was imported under " +
			"before.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"kind": {
					Type: chatdomain.TypeString,
					Description: "Optional. Narrow to card, bank_account or other. Omit to see " +
						"everything, which is the usual case.",
				},
			},
		},
	}
}

func (t importSourceList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	items, err := t.svc.ListImportSources(ctx, ws, 100)
	if err != nil {
		return nil, toolError(err)
	}
	kind := strings.TrimSpace(argString(args, "kind"))
	rows := make([]map[string]any, 0, len(items))
	for _, s := range items {
		if kind != "" && string(s.Kind) != kind {
			continue
		}
		row := map[string]any{
			"id": s.ID.String(), "kind": string(s.Kind),
			"institution": s.Institution, "label": s.Label,
		}
		if s.Last4 != nil {
			row["last4"] = *s.Last4
		}
		rows = append(rows, row)
	}
	return chatdomain.ToolOutput{"items": rows, "count": len(rows)}, nil
}

/* ── finance.import_source.create ────────────────────────────────────── */

type importSourceCreate struct{ base }

func (importSourceCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ImportSourceCreateTool,
		Title:        "Finance · Registrar fonte de importação",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Registers an account or card whose statements will be imported. Ask the " +
			"user first and use their words for the label: the id this returns becomes the " +
			"namespace every transaction imported through it is deduplicated against, and a " +
			"second source for the same real account would make the same statement importable " +
			"twice. Check finance.import_source.list before creating anything. " +
			"kind is card, bank_account or other.",
		Schema: chatdomain.ToolSchema{
			Required: []string{"kind", "institution", "label"},
			Properties: map[string]chatdomain.ToolProperty{
				"kind":        {Type: chatdomain.TypeString, Description: "Required. Exactly one of: card, bank_account, other."},
				"institution": {Type: chatdomain.TypeString, Description: "Required. The bank or issuer, e.g. \"Nubank\"."},
				"label":       {Type: chatdomain.TypeString, Description: "Required. The user's own name for it, unique in the workspace."},
				"last4":       {Type: chatdomain.TypeString, Description: "Optional. The last four digits, when there are any."},
			},
		},
	}
}

func (t importSourceCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	in := app.CreateImportSourceInput{
		WorkspaceID: ws,
		Kind:        domain.ImportSourceKind(strings.TrimSpace(argString(args, "kind"))),
		Institution: argString(args, "institution"),
		Label:       argString(args, "label"),
	}
	if v := strings.TrimSpace(argString(args, "last4")); v != "" {
		in.Last4 = &v
	}
	src, err := t.svc.CreateImportSource(ctx, in)
	if err != nil {
		return nil, toolError(err)
	}
	return chatdomain.ToolOutput{
		"id": src.ID.String(), "kind": string(src.Kind),
		"institution": src.Institution, "label": src.Label,
		"note": "Use this id as import_source_id. It is the identity duplicate detection is " +
			"namespaced by, and it never changes when the label does.",
	}, nil
}
