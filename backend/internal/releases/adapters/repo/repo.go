// Package repo is the driven adapter backing the releases ports with
// Postgres. Queries route through postgres.Conn(ctx, pool) so they
// participate in any TxManager scope, matching finance and chat.
//
// Note the absence that defines this package: no query here takes a
// workspace id. Releases describe the deployed build, not a tenant's data,
// and adding the column would let the same binary describe itself
// differently to two readers. The decision is recorded in
// migrations/releases/0001.
package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/releases/domain"
)

type Repositories struct {
	Releases *ReleaseRepo
}

func New(pool *pgxpool.Pool) *Repositories {
	return &Repositories{Releases: NewReleaseRepo(pool)}
}

type ReleaseRepo struct {
	pool *pgxpool.Pool
}

func NewReleaseRepo(pool *pgxpool.Pool) *ReleaseRepo { return &ReleaseRepo{pool: pool} }

const moduleCols = `key, name, description, status, position, created_at, updated_at`

func scanModule(row pgx.Row) (*domain.Module, error) {
	var m domain.Module
	if err := row.Scan(&m.Key, &m.Name, &m.Description, &m.Status, &m.Position, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *ReleaseRepo) ListModules(ctx context.Context) ([]*domain.Module, error) {
	q := `SELECT ` + moduleCols + ` FROM releases.modules ORDER BY position, key`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list modules: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Module, 0)
	for rows.Next() {
		m, err := scanModule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan module: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *ReleaseRepo) GetModule(ctx context.Context, key string) (*domain.Module, error) {
	q := `SELECT ` + moduleCols + ` FROM releases.modules WHERE key = $1`
	m, err := scanModule(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("module")
	}
	if err != nil {
		return nil, fmt.Errorf("get module: %w", err)
	}
	return m, nil
}

// releaseCols orders by the sortable integer columns, never by the version
// text. `ORDER BY version DESC` would place '1.9.0' above '1.10.0'.
const releaseCols = `id, module_key, version, status, stability, released_at, summary,
	capabilities, evidence, limitations, decisions, technical_notes, doc_refs,
	created_at, published_at`

const releaseOrder = ` ORDER BY major DESC, minor DESC, patch DESC`

func scanRelease(row pgx.Row) (*domain.Release, error) {
	var (
		rel                                 domain.Release
		caps, evid, lims, decs, notes, refs []byte
	)
	if err := row.Scan(
		&rel.ID, &rel.ModuleKey, &rel.Version, &rel.Status, &rel.Stability, &rel.ReleasedAt, &rel.Summary,
		&caps, &evid, &lims, &decs, &notes, &refs,
		&rel.CreatedAt, &rel.PublishedAt,
	); err != nil {
		return nil, err
	}
	// A snapshot that fails to decode is a corrupt historical record, and
	// the honest response is an error rather than an empty list: a release
	// page that silently shows "no capabilities" would be indistinguishable
	// from a release that genuinely shipped none.
	if err := decodeJSON(caps, &rel.Capabilities); err != nil {
		return nil, fmt.Errorf("release %s/%s capabilities: %w", rel.ModuleKey, rel.Version, err)
	}
	if err := decodeJSON(evid, &rel.Evidence); err != nil {
		return nil, fmt.Errorf("release %s/%s evidence: %w", rel.ModuleKey, rel.Version, err)
	}
	if err := decodeJSON(lims, &rel.Limitations); err != nil {
		return nil, fmt.Errorf("release %s/%s limitations: %w", rel.ModuleKey, rel.Version, err)
	}
	if err := decodeJSON(decs, &rel.Decisions); err != nil {
		return nil, fmt.Errorf("release %s/%s decisions: %w", rel.ModuleKey, rel.Version, err)
	}
	if err := decodeJSON(notes, &rel.TechnicalNotes); err != nil {
		return nil, fmt.Errorf("release %s/%s technical notes: %w", rel.ModuleKey, rel.Version, err)
	}
	if err := decodeJSON(refs, &rel.DocRefs); err != nil {
		return nil, fmt.Errorf("release %s/%s doc refs: %w", rel.ModuleKey, rel.Version, err)
	}
	// Timestamps go out in UTC, always.
	//
	// pgx hands back whatever zone the connection is in, so a release dated
	// 2026-08-12 00:00 UTC serializes as "2026-08-11T21:00:00-03:00" on a
	// server in São Paulo. That is the same instant and still the wrong
	// thing to publish: a release date is the headline fact of this API,
	// and an auditor reading the JSON should not have to convert it back to
	// find out which day it was. The instant never changes here — only the
	// zone it is rendered in.
	rel.CreatedAt = rel.CreatedAt.UTC()
	rel.ReleasedAt = utcPtr(rel.ReleasedAt)
	rel.PublishedAt = utcPtr(rel.PublishedAt)
	return &rel, nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// decodeJSON leaves the destination as an initialised empty slice when the
// column is NULL or empty, so the wire form is always a list.
func decodeJSON(raw []byte, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func (r *ReleaseRepo) ListReleases(ctx context.Context, moduleKey string) ([]*domain.Release, error) {
	q := `SELECT ` + releaseCols + ` FROM releases.releases WHERE module_key = $1` + releaseOrder
	return r.queryReleases(ctx, q, moduleKey)
}

func (r *ReleaseRepo) ListAllReleases(ctx context.Context) ([]*domain.Release, error) {
	q := `SELECT ` + releaseCols + ` FROM releases.releases` + releaseOrder
	return r.queryReleases(ctx, q)
}

func (r *ReleaseRepo) queryReleases(ctx context.Context, q string, args ...any) ([]*domain.Release, error) {
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Release, 0)
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

func (r *ReleaseRepo) GetRelease(ctx context.Context, moduleKey, version string) (*domain.Release, error) {
	q := `SELECT ` + releaseCols + ` FROM releases.releases WHERE module_key = $1 AND version = $2`
	rel, err := scanRelease(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, moduleKey, version))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("release")
	}
	if err != nil {
		return nil, err
	}
	return rel, nil
}

func (r *ReleaseRepo) CreateRelease(ctx context.Context, rel *domain.Release) error {
	v, err := rel.Parsed()
	if err != nil {
		return err
	}
	q := `INSERT INTO releases.releases
	        (module_key, version, major, minor, patch, status, stability, released_at, published_at,
	         summary, capabilities, evidence, limitations, decisions, technical_notes, doc_refs)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	      RETURNING id, created_at`
	err = postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		rel.ModuleKey, rel.Version, v.Major, v.Minor, v.Patch,
		rel.Status, rel.Stability, rel.ReleasedAt, rel.PublishedAt, rel.Summary,
		mustJSON(rel.Capabilities), mustJSON(rel.Evidence), mustJSON(rel.Limitations),
		mustJSON(rel.Decisions), mustJSON(rel.TechnicalNotes), mustJSON(rel.DocRefs),
	).Scan(&rel.ID, &rel.CreatedAt)
	if err != nil {
		return translate(err, rel)
	}
	return nil
}

func (r *ReleaseRepo) PublishRelease(ctx context.Context, rel *domain.Release) error {
	// The WHERE clause carries `status = 'draft'`, so two concurrent
	// publishes cannot both succeed: the second matches no row and is
	// reported as a conflict rather than silently overwriting the first
	// one's timestamp.
	q := `UPDATE releases.releases
	         SET status = $1, released_at = $2, published_at = $3
	       WHERE id = $4 AND status = 'draft'`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q,
		rel.Status, rel.ReleasedAt, rel.PublishedAt, rel.ID)
	if err != nil {
		return translate(err, rel)
	}
	if tag.RowsAffected() == 0 {
		return domain.Conflict(fmt.Sprintf("release %s/%s is no longer a draft", rel.ModuleKey, rel.Version))
	}
	return nil
}

func mustJSON(v any) []byte {
	// The inputs are the domain's own slices of plain structs. Failure here
	// would be a programming error, and an empty array keeps a broken value
	// from being written as SQL NULL against a NOT NULL column.
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}

// translate turns the constraints declared in the migration into domain
// errors, so the rules stated in SQL and the ones the API reports are the
// same rules rather than two descriptions that can disagree.
func translate(err error, rel *domain.Release) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch {
	case pg.Code == "23505" && pg.ConstraintName == "releases_unique_version":
		return domain.Conflict(fmt.Sprintf("release %s/%s already exists", rel.ModuleKey, rel.Version))
	case pg.Code == "23503":
		return domain.NotFound("module " + rel.ModuleKey)
	// restrict_violation is what the freeze trigger raises. Reaching it
	// means the application-level guard was bypassed, so it is surfaced as
	// the conflict it is rather than as a 500.
	case pg.Code == "23001":
		return domain.Conflict(pg.Message)
	}
	return err
}
