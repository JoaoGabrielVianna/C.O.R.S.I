package app

// Statement import: prepare, resolve, commit.
//
// ── Why three operations and not one ───────────────────────────────────
// Because the operator has to be able to say no in the middle. Preparing
// reads a file and decides what it thinks; committing changes the ledger.
// Anything that did both would import on the strength of a parse, and a
// parse is exactly the thing nobody has checked yet.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

// maxStatementRows bounds one batch. It is not a performance limit: it is
// the point past which a human can no longer audit what they approved.
const maxStatementRows = 1000

type PrepareImportInput struct {
	WorkspaceID uuid.UUID
	// ImportSourceID names a row this database issued. The dedup namespace
	// is DERIVED from it and never supplied.
	//
	// It used to be a free-text `account_scope`, and a live model produced
	// "nubank-4242" in one turn and "nubank-cartao-4242" in the next for
	// the same card, importing the same statement twice. No amount of
	// prompting fixes that; the argument had to stop existing.
	ImportSourceID uuid.UUID
	// SourceLabel is what the operator calls THIS import. Display only: it
	// is stored on the batch, never in an identity, so a model varying it
	// costs nothing.
	SourceLabel string
	Format      domain.ImportFormat
	Content     string
}

// PrepareImportResult is what the caller gets back: never the rows, which
// would not fit a tool result, and always the arithmetic.
type PrepareImportResult struct {
	Batch          *domain.ImportBatch
	Reconciliation domain.ImportReconciliation
	Groups         []ports.ImportGroup
	// Categories the workspace already has, carried in the same result.
	//
	// ── Why the preview hands these over ───────────────────────────────
	// Not for convenience. Every finance capability is Confidential, which
	// means the recorder drops its arguments and result — and the evidence
	// replayed into a later turn is read from exactly those records. So a
	// model cannot see what it observed in an earlier turn of this
	// conversation, and it re-derives the whole state every time.
	//
	// A turn allows three tool rounds. Re-deriving costs one round for the
	// preview and one for the resolutions, which leaves exactly one for the
	// commit. Spending a round on a separate category listing is what made
	// a live run loop forever without ever importing.
	//
	// This carries no decision: it is the same list finance.category.list
	// returns, and nothing here matches a category to a line.
	Categories []domain.Category
}

// PrepareImport parses, classifies and stages. It writes to the staging
// tables and to NOTHING else: no transaction is created, no total moves,
// and no financial read returns anything different afterwards.
func (s *Service) PrepareImport(ctx context.Context, in PrepareImportInput) (*PrepareImportResult, error) {
	if in.WorkspaceID == uuid.Nil {
		return nil, domain.Invalid("workspace_id required")
	}
	if in.ImportSourceID == uuid.Nil {
		return nil, domain.Invalid("import_source_id required")
	}
	// Resolved before anything else, and scoped to this workspace: a source
	// from another workspace is not found, which is also what a fabricated
	// id gets.
	source, err := s.repos.ImportSources.FindByID(ctx, in.WorkspaceID, in.ImportSourceID)
	if err != nil {
		return nil, err
	}
	// The namespace every imported row will carry, derived from the row's
	// id. Nothing the caller sent contributes to it.
	scope := source.ExternalSourceNamespace()

	label := strings.TrimSpace(in.SourceLabel)
	if label == "" {
		label = source.Label
	}
	if len(label) > 120 {
		label = label[:120]
	}
	if in.Format == "" {
		in.Format = domain.ImportFormatCSV
	}
	if !in.Format.Valid() {
		return nil, domain.Invalid("unsupported format")
	}

	// The same statement, staged again, is the same batch.
	//
	// A model re-derives its state every turn — Confidential capabilities
	// leave no result in the audit the evidence replay reads — so it
	// re-prepares a file it has already prepared and classified. Creating a
	// second batch would silently discard those classifications, and with
	// three tool rounds per turn the commit would never be reached.
	//
	// Matched on the content hash AND the account scope, which is why the
	// hash is stored: the same bytes for the same account is a fact, not an
	// inference. A committed batch is never reused, so this can only ever
	// resume work that is still open.
	if existing, err := s.repos.ImportBatches.FindPreparedByContent(
		ctx, in.WorkspaceID, scope, sha256Hex(in.Content)); err != nil {
		return nil, err
	} else if existing != nil {
		return s.importSnapshot(ctx, in.WorkspaceID, existing.ID)
	}

	lines, perr := parseCSV(in.Content)
	if perr != nil {
		return nil, perr
	}
	if len(lines) == 0 {
		return nil, domain.Invalid("the statement has no data lines")
	}
	if len(lines) > maxStatementRows {
		return nil, domain.Invalid(fmt.Sprintf(
			"the statement has %d lines; %d is the most one batch may carry",
			len(lines), maxStatementRows))
	}

	// Categories are resolved by EXACT normalised name, never by substring.
	// "MERCADO" must not silently become "Supermercado": a category chosen
	// by fuzzy match files real money under a label the operator did not
	// pick, and it looks correct in every screen afterwards.
	cats, err := s.repos.Categories.List(ctx, in.WorkspaceID, ports.CategoryFilter{Limit: 500})
	if err != nil {
		return nil, err
	}
	byName := make(map[string]domain.Category, len(cats))
	for _, c := range cats {
		byName[normaliseDescription(c.Name)] = c
	}

	// One round trip for the whole file's identities rather than one per
	// line: 150 queries would not fit the tool execution budget.
	ids := make([]string, 0, len(lines))
	for _, l := range lines {
		if l.Invalid {
			continue
		}
		if l.ExternalID != "" {
			ids = append(ids, l.ExternalID)
		} else {
			ids = append(ids, l.fingerprint(scope))
		}
	}
	existing, err := s.repos.ImportBatches.ExistingIdentities(ctx, in.WorkspaceID, scope, ids)
	if err != nil {
		return nil, err
	}

	batch := &domain.ImportBatch{
		ID:             uuid.New(),
		WorkspaceID:    in.WorkspaceID,
		SourceLabel:    label,
		AccountScope:   scope,
		ImportSourceID: &source.ID,
		Format:         in.Format,
		ContentSHA256:  sha256Hex(in.Content),
		RowCount:       len(lines),
	}

	rows := make([]domain.ImportRow, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, classify(l, batch, scope, byName, existing))
	}

	// Staging is written inside a transaction with the batch header, so a
	// batch never exists without its rows. A half-written batch would
	// report a reconciliation that does not balance and no way to tell why.
	if err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repos.ImportBatches.CreateBatch(ctx, batch); err != nil {
			return err
		}
		return s.repos.ImportBatches.InsertRows(ctx, rows)
	}); err != nil {
		return nil, err
	}

	return s.importSnapshot(ctx, in.WorkspaceID, batch.ID)
}

// classify turns one parsed line into a staged row.
//
// The order of the checks is the design. Invalid comes first because a
// line that could not be read has no identity to compare and no category
// to resolve. Duplicate comes before category because there is no point
// asking the operator to classify something already in the ledger.
func classify(l parsedLine, batch *domain.ImportBatch, scope string,
	byName map[string]domain.Category, existing map[string]bool) domain.ImportRow {

	row := domain.ImportRow{
		ID:          uuid.New(),
		BatchID:     batch.ID,
		WorkspaceID: batch.WorkspaceID,
		LineNumber:  l.LineNumber,
		Description: l.Description,
		GroupKey:    l.GroupKey,
	}

	if l.Invalid {
		row.State, row.StateDetail = domain.RowInvalid, l.Reason
		return row
	}

	occurred, amount, dir := l.OccurredAt, l.AmountCents, l.Direction
	row.OccurredAt, row.AmountCents, row.Direction = &occurred, &amount, &dir

	strategy := domain.IdentityHeuristic
	identity := l.fingerprint(scope)
	if l.ExternalID != "" {
		strategy, identity = domain.IdentityStrong, l.ExternalID
	}
	row.IdentityStrategy, row.IdentityValue = &strategy, &identity

	if existing[identity] {
		if strategy == domain.IdentityStrong {
			// The institution says this is the same event. Skipping needs no
			// permission.
			row.State = domain.RowDuplicate
			row.StateDetail = "already imported, matched by the institution's own identifier"
			return row
		}
		// A fingerprint match is a QUESTION, not an answer. Two identical
		// purchases on the same day are indistinguishable from one purchase
		// exported twice, and discarding the second would delete a real
		// transaction that nobody would ever miss.
		row.State = domain.RowAmbiguousDuplicate
		row.StateDetail = "looks like a line already imported, but only by date, amount and description; " +
			"it could equally be a second identical purchase"
		return row
	}

	if looksInternal(l.GroupKey) {
		// A statement shows ONE leg. Finance models a transfer as two rows
		// with opposite categories, so classifying this alone would either
		// invent the counterparty or inflate expense with a movement that
		// never left the operator's own money.
		row.State = domain.RowAmbiguousInternal
		row.StateDetail = "may be a movement between your own accounts; a statement only shows one side"
		return row
	}

	if cat, ok := byName[l.GroupKey]; ok && cat.Type == dir {
		row.CategoryID = &cat.ID
		row.State = domain.RowReady
		return row
	}

	row.State = domain.RowUnresolvedCategory
	row.StateDetail = "no category with this exact name"
	return row
}

type ResolveImportGroupInput struct {
	WorkspaceID uuid.UUID
	BatchID     uuid.UUID
	GroupKey    string
	CategoryID  uuid.UUID
}

// ResolveImportGroup attaches an operator-chosen category to every
// unresolved line sharing a normalised description.
//
// Resolution is per GROUP because a tool schema in this system is a flat
// object of scalars: it can carry one group key and one category id, not a
// map of 150 descriptions. It is also how 150 lines become roughly nine
// decisions a person can actually make.
// resolveBatchID turns "the batch we were talking about" into an id.
//
// ── Why a default exists at all ────────────────────────────────────────
// The tool schema in this system is a flat object of scalars, so a batch
// is addressed by a 36-character uuid the model has to carry across
// several conversational turns. The first live run of this flow proved
// that it does not survive the trip: the model produced a string that was
// not a uuid at all, and the commit was refused.
//
// Refusing was correct, and being refused for that reason is a bad way for
// an import to fail. So an omitted id means the most recent batch still
// awaiting a decision IN THIS WORKSPACE, which is a fact the database
// holds rather than one the model has to remember.
//
// ── Why this is not a widening of access ───────────────────────────────
// The lookup is workspace-scoped by the same predicate FindBatch uses. A
// caller cannot reach another workspace's batch by omitting the id any
// more than by naming it, and a batch already committed is not 'prepared'
// so it is never what "the latest" means.
func (s *Service) resolveBatchID(ctx context.Context, workspaceID uuid.UUID, given uuid.UUID) (uuid.UUID, error) {
	if given != uuid.Nil {
		return given, nil
	}
	b, err := s.repos.ImportBatches.LatestPrepared(ctx, workspaceID)
	if err != nil {
		return uuid.Nil, err
	}
	return b.ID, nil
}

func (s *Service) ResolveImportGroup(ctx context.Context, in ResolveImportGroupInput) (*PrepareImportResult, error) {
	batchID, err := s.resolveBatchID(ctx, in.WorkspaceID, in.BatchID)
	if err != nil {
		return nil, err
	}
	in.BatchID = batchID
	batch, err := s.repos.ImportBatches.FindBatch(ctx, in.WorkspaceID, in.BatchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != domain.ImportBatchPrepared {
		return nil, domain.Conflict("this batch has already been committed")
	}
	key := normaliseDescription(in.GroupKey)
	if key == "" {
		return nil, domain.Invalid("group_key required")
	}
	// Resolved through the categories table, scoped to this workspace and
	// matched on direction: an income category cannot be attached to an
	// expense line, which is the composite foreign key's rule applied
	// before the row reaches it.
	n, err := s.repos.ImportBatches.ResolveGroup(ctx, in.WorkspaceID, in.BatchID, key, in.CategoryID)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Nothing moved. That has two very different causes, and telling
		// them apart matters: repeating a classification already made is
		// success, while naming a group or a category that does not fit is
		// a mistake the caller has to see.
		done, derr := s.repos.ImportBatches.GroupAlreadyResolvedTo(ctx, in.WorkspaceID, in.BatchID, key, in.CategoryID)
		if derr != nil {
			return nil, derr
		}
		if !done {
			return nil, domain.Invalid("nothing to resolve: either no line has that description, " +
				"or the category does not exist in this workspace with the matching direction")
		}
	}
	return s.importSnapshot(ctx, in.WorkspaceID, in.BatchID)
}

type CommitImportInput struct {
	WorkspaceID uuid.UUID
	BatchID     uuid.UUID
}

// CommitImport materialises the ready rows, and only those.
//
// ── What it never imports ──────────────────────────────────────────────
// invalid, unresolved_category, ambiguous_internal and unresolved
// ambiguous_duplicate. Those are not failures of the import; they are the
// questions it could not answer alone, and they stay staged so the
// reconciliation keeps naming them.
func (s *Service) CommitImport(ctx context.Context, in CommitImportInput) (*domain.ImportResult, error) {
	batchID, err := s.resolveBatchID(ctx, in.WorkspaceID, in.BatchID)
	if err != nil {
		return nil, err
	}
	in.BatchID = batchID
	batch, err := s.repos.ImportBatches.FindBatch(ctx, in.WorkspaceID, in.BatchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != domain.ImportBatchPrepared {
		return nil, domain.Conflict("this batch has already been committed")
	}

	var result domain.ImportResult
	// One transaction around the whole commit. An infrastructure failure
	// rolls back every row: the ledger either gained this statement or
	// gained nothing from it, and there is no state where half a month of
	// spending is in and half is not.
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		rec, err := s.repos.ImportBatches.Reconcile(ctx, in.WorkspaceID, in.BatchID)
		if err != nil {
			return err
		}
		if !rec.Balances() {
			// A reconciliation that does not add up is a bug in this module,
			// not bad input, so it aborts rather than reporting a number
			// nobody should trust.
			return domain.Invalid("the batch does not reconcile and will not be committed")
		}
		created, err := s.repos.ImportBatches.MaterialiseReady(ctx, in.WorkspaceID, in.BatchID, batch.AccountScope)
		if err != nil {
			return err
		}
		skipped := rec.Ready - created
		if skipped < 0 {
			return domain.Invalid("more rows were created than were eligible")
		}
		if err := s.repos.ImportBatches.MarkCommitted(ctx, in.WorkspaceID, in.BatchID, created, skipped); err != nil {
			return err
		}
		result = domain.ImportResult{
			BatchID:           in.BatchID,
			EligibleReady:     rec.Ready,
			Created:           created,
			SkippedConcurrent: skipped,
			Reconciliation:    rec,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetImportBatch is the readback: the same arithmetic, after the fact.
func (s *Service) GetImportBatch(ctx context.Context, workspaceID, batchID uuid.UUID) (*PrepareImportResult, error) {
	return s.importSnapshot(ctx, workspaceID, batchID)
}

func (s *Service) importSnapshot(ctx context.Context, workspaceID, batchID uuid.UUID) (*PrepareImportResult, error) {
	batch, err := s.repos.ImportBatches.FindBatch(ctx, workspaceID, batchID)
	if err != nil {
		return nil, err
	}
	rec, err := s.repos.ImportBatches.Reconcile(ctx, workspaceID, batchID)
	if err != nil {
		return nil, err
	}
	groups, err := s.repos.ImportBatches.UnresolvedGroups(ctx, workspaceID, batchID, 25)
	if err != nil {
		return nil, err
	}
	cats, err := s.repos.Categories.List(ctx, workspaceID, ports.CategoryFilter{Limit: 200})
	if err != nil {
		return nil, err
	}
	return &PrepareImportResult{Batch: batch, Reconciliation: rec, Groups: groups, Categories: cats}, nil
}

/* ── import sources ──────────────────────────────────────────────────── */

type CreateImportSourceInput struct {
	WorkspaceID uuid.UUID
	Kind        domain.ImportSourceKind
	Institution string
	Label       string
	Last4       *string
	CardID      *uuid.UUID
}

// CreateImportSource registers where statements come from.
//
// Deliberately an explicit act by the operator, like creating a category:
// the id it returns becomes the namespace every transaction imported
// through it is deduplicated against, and something with that much
// consequence should not appear as a side effect of an import.
func (s *Service) CreateImportSource(ctx context.Context, in CreateImportSourceInput) (*domain.ImportSource, error) {
	src := &domain.ImportSource{
		WorkspaceID: in.WorkspaceID,
		Kind:        in.Kind,
		Institution: in.Institution,
		Label:       in.Label,
		Last4:       in.Last4,
		CardID:      in.CardID,
	}
	if err := src.Validate(); err != nil {
		return nil, err
	}
	if src.CardID != nil {
		// Resolved in this workspace first: the foreign key alone would
		// accept another workspace's card.
		if _, err := s.repos.Cards.FindByID(ctx, in.WorkspaceID, *src.CardID); err != nil {
			return nil, err
		}
	}
	if err := s.repos.ImportSources.Create(ctx, src); err != nil {
		return nil, err
	}
	return src, nil
}

func (s *Service) ListImportSources(ctx context.Context, workspaceID uuid.UUID, limit int) ([]domain.ImportSource, error) {
	return s.repos.ImportSources.List(ctx, workspaceID, limit)
}
