// Package repo is the Postgres implementation of the Job Radar storage
// ports.
//
// Every query is workspace-first, for the reason the ports package states:
// an id that arrives from a URL or from a model must not be able to reach a
// row it does not own, and the predicate that guarantees it belongs in the
// SQL rather than in a check somebody has to remember to write.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/jobradar/domain"
	"github.com/corsi/backend/internal/jobradar/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

// Repos bundles the implementations so the module wires one value.
type Repos struct {
	Opportunities ports.OpportunityRepo
	Companies     ports.CompanyRepo
}

func New(pool *pgxpool.Pool) *Repos {
	txm := postgres.NewTxManager(pool)
	return &Repos{
		Opportunities: &OpportunityRepo{pool: pool, txm: txm},
		Companies:     &CompanyRepo{pool: pool},
	}
}

// defaultLimit bounds a listing that did not ask for one.
//
// Fifty is the board's realistic working set. It matters most for the tool
// path: every row returned there becomes prompt tokens on the next provider
// call, so an unbounded default would let one `list` call spend a turn's
// whole budget on rows the model did not need.
const defaultLimit = 50

// maxLimit is the ceiling a caller cannot argue past.
const maxLimit = 200

/* ── companies ───────────────────────────────────────────────────────── */

type CompanyRepo struct{ pool *pgxpool.Pool }

const companyCols = `id, workspace_id, name, domain, created_at, updated_at`

func scanCompany(row pgx.Row) (*domain.Company, error) {
	var c domain.Company
	if err := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Domain, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// FindOrCreate resolves a name to a company row.
//
// ── Why the insert comes first ─────────────────────────────────────────
// A read-then-write would have a window between the two in which another
// request inserts the same employer, and both callers would then believe
// they created it. `ON CONFLICT DO NOTHING` closes that window in the
// database: the insert either wins or does nothing, and the SELECT that
// follows returns the winner either way. The conflict target is the
// expression index itself, which is what makes "Stripe" and "stripe" one
// company rather than two.
//
// ── Why the domain is filled and never overwritten ─────────────────────
// `DO UPDATE ... WHERE companies.domain = ”` is the whole rule in one
// clause: a row that has no domain learns one, and a row that has one keeps
// it. An import carrying a blank must not erase what somebody typed, and a
// legacy browser document is not a more authoritative source than the
// record currently in front of the user.
func (r *CompanyRepo) FindOrCreate(ctx context.Context, workspaceID uuid.UUID, name, companyDomain string) (*domain.Company, error) {
	clean, err := domain.NormalizeCompanyName(name)
	if err != nil {
		return nil, err
	}
	companyDomain = strings.TrimSpace(companyDomain)

	if _, err := r.pool.Exec(ctx, `
		INSERT INTO jobradar.companies (workspace_id, name, domain)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, lower(name)) DO UPDATE
			SET domain = EXCLUDED.domain
			WHERE jobradar.companies.domain = '' AND EXCLUDED.domain <> ''`,
		workspaceID, clean, companyDomain); err != nil {
		return nil, fmt.Errorf("jobradar: insert company: %w", err)
	}

	c, err := scanCompany(r.pool.QueryRow(ctx, `
		SELECT `+companyCols+`
		FROM jobradar.companies
		WHERE workspace_id = $1 AND lower(name) = lower($2)`,
		workspaceID, clean))
	if err != nil {
		return nil, fmt.Errorf("jobradar: read company: %w", err)
	}
	return c, nil
}

func (r *CompanyRepo) List(ctx context.Context, workspaceID uuid.UUID) ([]*domain.Company, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+companyCols+`
		FROM jobradar.companies
		WHERE workspace_id = $1
		ORDER BY name`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("jobradar: list companies: %w", err)
	}
	defer rows.Close()

	var out []*domain.Company
	for rows.Next() {
		c, err := scanCompany(rows)
		if err != nil {
			return nil, fmt.Errorf("jobradar: scan company: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CompanyRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Company, error) {
	c, err := scanCompany(r.pool.QueryRow(ctx, `
		SELECT `+companyCols+`
		FROM jobradar.companies
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("company %s was not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("jobradar: read company: %w", err)
	}
	return c, nil
}

/* ── opportunities ───────────────────────────────────────────────────── */

type OpportunityRepo struct {
	pool *pgxpool.Pool
	txm  *postgres.TxManager
}

// opportunityCols is the projection every read shares, company name
// included. `c.name` is joined rather than denormalised so renaming an
// employer does not require rewriting its opportunities.
const opportunityCols = `
	o.id, o.workspace_id, o.company_id, c.name,
	o.role, o.salary, o.location, o.stack, o.description,
	o.source, o.source_url, o.match_percent, o.posted_at,
	o.stage, o.tracked_at, o.stage_entered_at, o.next_action,
	o.notes_general, o.notes_interview, o.notes_technical, o.notes_personal,
	o.created_at, o.updated_at`

func scanOpportunity(row pgx.Row) (*domain.Opportunity, error) {
	var o domain.Opportunity
	// The tracking columns are read into pointers because all three are NULL
	// together for a record in Discover — the CHECK constraint in the
	// migration is what makes "all three or none" safe to assume here.
	var (
		stage          *string
		trackedAt      *time.Time
		stageEnteredAt *time.Time
		nextAction     string
	)
	if err := row.Scan(
		&o.ID, &o.WorkspaceID, &o.CompanyID, &o.CompanyName,
		&o.Role, &o.Salary, &o.Location, &o.Stack, &o.Description,
		&o.Source, &o.SourceURL, &o.MatchPercent, &o.PostedAt,
		&stage, &trackedAt, &stageEnteredAt, &nextAction,
		&o.Notes.General, &o.Notes.Interview, &o.Notes.Technical, &o.Notes.Personal,
		&o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if stage != nil && trackedAt != nil && stageEnteredAt != nil {
		o.Tracking = &domain.Tracking{
			Stage:          domain.PipelineStage(*stage),
			TrackedAt:      *trackedAt,
			StageEnteredAt: *stageEnteredAt,
			NextAction:     nextAction,
		}
	}
	if o.Stack == nil {
		// An empty array and a NULL both mean "no stack". Normalising here
		// keeps the JSON `[]` rather than `null`, so a client never has to
		// tell two spellings of nothing apart.
		o.Stack = []string{}
	}
	return &o, nil
}

// where builds the filter's predicates and arguments together.
//
// Returning both from one function is what keeps the placeholder numbers
// and the argument slice in step: two functions that each knew the order
// would be one edit away from `$4` reading argument five.
func where(workspaceID uuid.UUID, f ports.OpportunityFilter) (string, []any) {
	args := []any{workspaceID}
	clauses := []string{"o.workspace_id = $1", "o.deleted_at IS NULL"}

	// bind appends one argument and returns its placeholder number, so a
	// clause that mentions the same value several times can reuse it
	// instead of binding it again.
	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	if s := strings.TrimSpace(f.Company); s != "" {
		clauses = append(clauses, fmt.Sprintf("c.name ILIKE '%%' || $%d || '%%'", bind(s)))
	}
	if f.Stage != nil {
		clauses = append(clauses, fmt.Sprintf("o.stage = $%d", bind(string(*f.Stage))))
	}
	if f.Untracked {
		clauses = append(clauses, "o.stage IS NULL")
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		n := bind(s)
		clauses = append(clauses, fmt.Sprintf(
			"(o.role ILIKE '%%' || $%d || '%%'"+
				" OR c.name ILIKE '%%' || $%d || '%%'"+
				" OR o.location ILIKE '%%' || $%d || '%%')", n, n, n))
	}
	return strings.Join(clauses, " AND "), args
}

func (r *OpportunityRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.OpportunityFilter) ([]*domain.Opportunity, error) {
	predicate, args := where(workspaceID, f)

	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	args = append(args, limit)
	limitPlaceholder := len(args)
	args = append(args, f.Offset)
	offsetPlaceholder := len(args)

	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM jobradar.opportunities o
		JOIN jobradar.companies c ON c.id = o.company_id
		WHERE %s
		ORDER BY o.created_at DESC, o.id
		LIMIT $%d OFFSET $%d`,
		opportunityCols, predicate, limitPlaceholder, offsetPlaceholder), args...)
	if err != nil {
		return nil, fmt.Errorf("jobradar: list opportunities: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Opportunity, 0, limit)
	for rows.Next() {
		o, err := scanOpportunity(rows)
		if err != nil {
			return nil, fmt.Errorf("jobradar: scan opportunity: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *OpportunityRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.OpportunityFilter) (int64, error) {
	predicate, args := where(workspaceID, f)
	var n int64
	if err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*)
		FROM jobradar.opportunities o
		JOIN jobradar.companies c ON c.id = o.company_id
		WHERE %s`, predicate), args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("jobradar: count opportunities: %w", err)
	}
	return n, nil
}

func (r *OpportunityRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Opportunity, error) {
	o, err := scanOpportunity(r.pool.QueryRow(ctx, `
		SELECT `+opportunityCols+`
		FROM jobradar.opportunities o
		JOIN jobradar.companies c ON c.id = o.company_id
		WHERE o.workspace_id = $1 AND o.id = $2 AND o.deleted_at IS NULL`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		// Deliberately the same answer as "belongs to another workspace".
		// See domain.KindNotFound.
		return nil, domain.NotFound("opportunity %s was not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("jobradar: read opportunity: %w", err)
	}
	return o, nil
}

func (r *OpportunityRepo) Create(ctx context.Context, o *domain.Opportunity) error {
	var (
		stage          *string
		trackedAt      *time.Time
		stageEnteredAt *time.Time
		nextAction     string
	)
	if o.Tracking != nil {
		s := string(o.Tracking.Stage)
		stage, trackedAt, stageEnteredAt = &s, &o.Tracking.TrackedAt, &o.Tracking.StageEnteredAt
		nextAction = o.Tracking.NextAction
	}

	err := r.pool.QueryRow(ctx, `
		INSERT INTO jobradar.opportunities (
			workspace_id, company_id, role, salary, location, stack, description,
			source, source_url, match_percent, posted_at,
			stage, tracked_at, stage_entered_at, next_action,
			notes_general, notes_interview, notes_technical, notes_personal)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING id, created_at, updated_at`,
		o.WorkspaceID, o.CompanyID, o.Role, o.Salary, o.Location, o.Stack, o.Description,
		o.Source, o.SourceURL, o.MatchPercent, o.PostedAt,
		stage, trackedAt, stageEnteredAt, nextAction,
		o.Notes.General, o.Notes.Interview, o.Notes.Technical, o.Notes.Personal,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("jobradar: create opportunity: %w", err)
	}
	return nil
}

// FindByImportIdentity resolves a legacy record to the row it became.
//
// No `deleted_at IS NULL` predicate, and that absence is the design: see
// the port. A soft-deleted import is still an import that happened, and
// re-running the migration must not undo a deletion.
func (r *OpportunityRepo) FindByImportIdentity(ctx context.Context, workspaceID uuid.UUID, id domain.ImportIdentity) (*domain.Opportunity, error) {
	if id.Zero() {
		return nil, nil
	}
	o, err := scanOpportunity(r.pool.QueryRow(ctx, `
		SELECT `+opportunityCols+`
		FROM jobradar.opportunities o
		JOIN jobradar.companies c ON c.id = o.company_id
		WHERE o.workspace_id = $1 AND o.import_source = $2 AND o.import_external_id = $3`,
		workspaceID, id.Source, id.ExternalID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("jobradar: find by import identity: %w", err)
	}
	return o, nil
}

// ImportCreate writes a migrated opportunity and its whole timeline at once.
//
// ── Why created_at and updated_at are written explicitly ───────────────
// Every other write lets the database stamp them, and that is correct for
// everything that happens now. An import is a statement about a past: the
// records being migrated have been sitting in their stages for weeks, and
// letting `now()` win would reset every clock at once — "12 days in
// Applied", the number the board exists to show, would read 0 for the whole
// pipeline on migration day. This is the only path allowed to say otherwise.
//
// ── Why the timeline is in the same transaction ────────────────────────
// The row and the history of how it got there are one fact. A crash between
// them leaves an opportunity whose past does not explain its present, and
// unlike a failed insert that is not something a retry notices. Together or
// not at all; the import identity is what makes the retry safe.
func (r *OpportunityRepo) ImportCreate(ctx context.Context, o *domain.Opportunity, timeline []domain.StageEvent) error {
	var (
		stage          *string
		trackedAt      *time.Time
		stageEnteredAt *time.Time
		nextAction     string
	)
	if o.Tracking != nil {
		s := string(o.Tracking.Stage)
		stage, trackedAt, stageEnteredAt = &s, &o.Tracking.TrackedAt, &o.Tracking.StageEnteredAt
		nextAction = o.Tracking.NextAction
	}

	return r.txm.WithinTx(ctx, func(ctx context.Context) error {
		conn := postgres.Conn(ctx, r.pool)

		// COALESCE keeps the ordinary default for a record whose legacy
		// document carried no timestamp: a zero time means "unknown", and
		// writing year 1 would sort it below everything forever.
		err := conn.QueryRow(ctx, `
			INSERT INTO jobradar.opportunities (
				workspace_id, company_id, role, salary, location, stack, description,
				source, source_url, match_percent, posted_at,
				stage, tracked_at, stage_entered_at, next_action,
				notes_general, notes_interview, notes_technical, notes_personal,
				import_source, import_external_id,
				created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,
			        COALESCE($22, now()), COALESCE($23, now()))
			RETURNING id, created_at, updated_at`,
			o.WorkspaceID, o.CompanyID, o.Role, o.Salary, o.Location, o.Stack, o.Description,
			o.Source, o.SourceURL, o.MatchPercent, o.PostedAt,
			stage, trackedAt, stageEnteredAt, nextAction,
			o.Notes.General, o.Notes.Interview, o.Notes.Technical, o.Notes.Personal,
			o.Import.Source, o.Import.ExternalID,
			nullableTime(o.CreatedAt), nullableTime(o.UpdatedAt),
		).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
		if err != nil {
			return fmt.Errorf("jobradar: import opportunity: %w", err)
		}

		for i := range timeline {
			var from *string
			if timeline[i].FromStage != nil {
				f := string(*timeline[i].FromStage)
				from = &f
			}
			if err := conn.QueryRow(ctx, `
				INSERT INTO jobradar.stage_events (workspace_id, opportunity_id, from_stage, to_stage, occurred_at)
				VALUES ($1, $2, $3, $4, $5)
				RETURNING id`,
				o.WorkspaceID, o.ID, from, string(timeline[i].ToStage), timeline[i].OccurredAt,
			).Scan(&timeline[i].ID); err != nil {
				return fmt.Errorf("jobradar: import stage event: %w", err)
			}
		}
		return nil
	})
}

// nullableTime turns the zero time into a SQL NULL, so the column default
// applies instead of the year 1.
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// Update writes the mutable fields and stamps updated_at.
//
// The tracking columns are absent from this statement on purpose — see the
// port's note. A stage moves through Move, which also writes the event.
func (r *OpportunityRepo) Update(ctx context.Context, o *domain.Opportunity) error {
	nextAction := ""
	if o.Tracking != nil {
		nextAction = o.Tracking.NextAction
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE jobradar.opportunities SET
			company_id = $3, role = $4, salary = $5, location = $6, stack = $7,
			description = $8, source = $9, source_url = $10, match_percent = $11,
			posted_at = $12, next_action = $13,
			notes_general = $14, notes_interview = $15, notes_technical = $16,
			notes_personal = $17, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		o.WorkspaceID, o.ID, o.CompanyID, o.Role, o.Salary, o.Location, o.Stack,
		o.Description, o.Source, o.SourceURL, o.MatchPercent,
		o.PostedAt, nextAction,
		o.Notes.General, o.Notes.Interview, o.Notes.Technical, o.Notes.Personal)
	if err != nil {
		return fmt.Errorf("jobradar: update opportunity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("opportunity %s was not found", o.ID)
	}
	return nil
}

// Move persists a transition and its event together.
//
// ── Why one transaction ────────────────────────────────────────────────
// The new stage and the record of having entered it are one fact. A crash
// between the two would leave an opportunity whose history does not explain
// where it is — and since the history is what the board's "time in stage"
// and any later analysis read, a silent gap there is worse than a failed
// move, which the caller can retry.
func (r *OpportunityRepo) Move(ctx context.Context, o *domain.Opportunity, event *domain.StageEvent) error {
	if o.Tracking == nil {
		return fmt.Errorf("jobradar: move called with no tracking state")
	}
	return r.txm.WithinTx(ctx, func(ctx context.Context) error {
		conn := postgres.Conn(ctx, r.pool)

		tag, err := conn.Exec(ctx, `
			UPDATE jobradar.opportunities SET
				stage = $3, tracked_at = $4, stage_entered_at = $5, updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
			o.WorkspaceID, o.ID, string(o.Tracking.Stage),
			o.Tracking.TrackedAt, o.Tracking.StageEnteredAt)
		if err != nil {
			return fmt.Errorf("jobradar: move opportunity: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.NotFound("opportunity %s was not found", o.ID)
		}

		var from *string
		if event.FromStage != nil {
			s := string(*event.FromStage)
			from = &s
		}
		if err := conn.QueryRow(ctx, `
			INSERT INTO jobradar.stage_events (workspace_id, opportunity_id, from_stage, to_stage, occurred_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id`,
			o.WorkspaceID, o.ID, from, string(event.ToStage), event.OccurredAt,
		).Scan(&event.ID); err != nil {
			return fmt.Errorf("jobradar: record stage event: %w", err)
		}

		// Read updated_at back so the caller reports the timestamp the
		// database wrote rather than the one it hoped for.
		if err := conn.QueryRow(ctx, `
			SELECT updated_at FROM jobradar.opportunities WHERE workspace_id = $1 AND id = $2`,
			o.WorkspaceID, o.ID).Scan(&o.UpdatedAt); err != nil {
			return fmt.Errorf("jobradar: read updated_at: %w", err)
		}
		return nil
	})
}

func (r *OpportunityRepo) Untrack(ctx context.Context, workspaceID, id uuid.UUID) error {
	// All three columns clear together — the CHECK constraint would refuse
	// anything else, which is the point of having written it.
	tag, err := r.pool.Exec(ctx, `
		UPDATE jobradar.opportunities
		SET stage = NULL, tracked_at = NULL, stage_entered_at = NULL,
		    next_action = '', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, workspaceID, id)
	if err != nil {
		return fmt.Errorf("jobradar: untrack opportunity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("opportunity %s was not found", id)
	}
	return nil
}

func (r *OpportunityRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE jobradar.opportunities SET deleted_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, workspaceID, id)
	if err != nil {
		return fmt.Errorf("jobradar: delete opportunity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("opportunity %s was not found", id)
	}
	return nil
}

func (r *OpportunityRepo) ListStageEvents(ctx context.Context, workspaceID, opportunityID uuid.UUID) ([]domain.StageEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, from_stage, to_stage, occurred_at
		FROM jobradar.stage_events
		WHERE workspace_id = $1 AND opportunity_id = $2
		ORDER BY occurred_at, id`, workspaceID, opportunityID)
	if err != nil {
		return nil, fmt.Errorf("jobradar: list stage events: %w", err)
	}
	defer rows.Close()

	out := []domain.StageEvent{}
	for rows.Next() {
		var (
			e    domain.StageEvent
			from *string
		)
		if err := rows.Scan(&e.ID, &from, &e.ToStage, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("jobradar: scan stage event: %w", err)
		}
		if from != nil {
			s := domain.PipelineStage(*from)
			e.FromStage = &s
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
