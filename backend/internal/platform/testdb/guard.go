//go:build integration

// Package testdb answers one question, before anything irreversible happens:
// is this database one we are allowed to destroy?
//
// ── The accident it exists to prevent ──────────────────────────────────
// The integration suites reset their schema before every test — migrate
// down, DROP SCHEMA, or both. Which database they reset comes from
// TEST_POSTGRES_DSN, an environment variable. Point it at the development
// database, run the suite, and the development database is gone. That is
// not hypothetical: it happened, and it took chat.providers with it,
// including the sealed LiteLLM credential, which existed nowhere else.
//
// ── Why a marker table and not a rule about the DSN ────────────────────
// Every rule about the *string* fails, and each fails in a way that is
// invisible until it matters:
//
//   - A required `_test` suffix is a rule about a name. Names are chosen by
//     whoever creates the database, so a developer database called
//     `corsi_test` would be destroyable, and the GitHub suite's own
//     generated names (`corsi_github_<nanos>`) would not be.
//   - "Refuse if the DSN equals the app's DSN" compares two strings that
//     routinely differ while naming the same database: localhost against
//     127.0.0.1, a trailing sslmode, a password spelled or omitted. The
//     comparison passes and the database still dies.
//   - An opt-in environment variable is exported once and then lives in the
//     shell for the rest of the day. It protects the first run and nothing
//     after it.
//
// The marker is a property OF THE DATABASE. It does not care how the
// database was addressed, which shell asked, or what anybody named it. A
// development database has never had it, because nothing but a throwaway
// provisioner creates it — and a provisioner marks a database at the moment
// it creates it, when "this one is disposable" is a fact rather than a
// belief.
//
// ── Why this package is behind the integration tag ─────────────────────
// It exists to serve tests and nothing else. Without the tag it would be
// compiled into the production binary, and a guard against destroying
// databases is not a capability the API should be shipping.
package testdb

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// MarkerTable is the table whose existence means "this database may be
// dropped and rebuilt".
//
// In `public`, unqualified, because the suites reset named schemas and the
// marker must not be inside anything they reset. It has no columns worth
// reading: presence is the whole signal, and a column would invite somebody
// to store a condition in it.
const MarkerTable = "public.destructible_database"

// checkTimeout bounds the one round trip this package makes. A guard that
// can hang is a guard somebody removes.
const checkTimeout = 10 * time.Second

// Destructible reports whether the database at dsn carries the marker.
//
// It performs exactly one read and mutates nothing, which is what lets a
// caller ask before deciding rather than after. A connection failure is
// returned as an error rather than as "not destructible", because the two
// call for different responses and conflating them would report an
// unreachable database as a protected one.
func Destructible(ctx context.Context, dsn string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return false, fmt.Errorf("connect to check the destructible marker: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	var present bool
	// to_regclass answers without raising when the table is absent, so the
	// ordinary "not marked" case is a value rather than an error to parse.
	if err := conn.QueryRow(ctx,
		`SELECT to_regclass($1) IS NOT NULL`, MarkerTable).Scan(&present); err != nil {
		return false, fmt.Errorf("check the destructible marker: %w", err)
	}
	return present, nil
}

// Mark makes a database destructible.
//
// Called by whatever provisions a throwaway database, at the moment it
// creates one. Never called by a test: a suite that could mark its own
// target could mark the development database, which is the entire failure
// this package prevents.
func Mark(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to write the destructible marker: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if _, err := conn.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS `+MarkerTable+` (
		   marked_at TIMESTAMPTZ NOT NULL DEFAULT now()
		 )`); err != nil {
		return fmt.Errorf("write the destructible marker: %w", err)
	}
	return nil
}

// Fataler is the only thing this guard is allowed to do to a test: stop it,
// loudly.
//
// ── Why the parameter is this and not testing.TB ───────────────────────
// Because testing.TB also has Skipf, and Skipf is the one wrong answer
// here. A suite aimed at the wrong database has been MISCONFIGURED; that is
// a mistake somebody must see and fix. Skipping would let the run report
// success while nothing was tested and nothing was checked — the worst
// possible outcome, and the one that looks exactly like the best.
//
// Narrowing the parameter turns "do not downgrade this to a skip" from a
// comment somebody can ignore into a compile error. *testing.T satisfies
// it; a rewrite that reaches for t.Skipf does not build.
type Fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AssertDestructible stops the test unless the database may be destroyed.
//
// Call it BEFORE the first destructive statement. It reads and nothing
// else, so a refusal leaves the database exactly as it found it — which is
// the property that makes the guard worth having at all.
func AssertDestructible(t Fataler, dsn string) {
	t.Helper()

	ok, err := Destructible(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database safety guard: %v", err)
	}
	if !ok {
		t.Fatalf(`database safety guard: REFUSING to reset this database.

  TEST_POSTGRES_DSN points at a database with no %s table.
  This suite drops and re-applies schemas, so running it here would
  destroy whatever is in it. Nothing has been changed.

  If this is a throwaway database, let the harness provision it:
      make -C backend test-integration-isolated
  That is also what 'make ci' runs.

  If you are bringing your own database and are willing to lose it,
  mark it yourself, once:
      CREATE TABLE %s (marked_at TIMESTAMPTZ NOT NULL DEFAULT now());
  Never do that to a database you care about.`, MarkerTable, MarkerTable)
	}
}
