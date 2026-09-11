//go:build integration

// The Database Safety Guard, tested against real databases.
//
// Every case here creates its own database, does something to it, and drops
// it. Nothing touches the database the suite was pointed at except to
// connect and issue CREATE DATABASE — which is why this file can assert
// "the guard refused and changed nothing" honestly.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping database safety guard tests")
	}
	return v
}

func withDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// newDatabase creates an empty database and returns its DSN. `marked` says
// whether it gets the destructible marker, which is the single variable
// almost every case here turns on.
func newDatabase(t *testing.T, prefix string, marked bool) string {
	t.Helper()
	admin := adminDSN(t)
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	name := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	_ = conn.Close(ctx)

	dsn := withDatabase(t, admin, name)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(),
			`DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	})

	if marked {
		if err := Mark(ctx, dsn); err != nil {
			t.Fatalf("mark: %v", err)
		}
	}
	return dsn
}

func exec(t *testing.T, dsn, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func scalar[T any](t *testing.T, dsn, sql string) T {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var out T
	if err := conn.QueryRow(ctx, sql).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}

func destructible(t *testing.T, dsn string) bool {
	t.Helper()
	ok, err := Destructible(context.Background(), dsn)
	if err != nil {
		t.Fatalf("Destructible: %v", err)
	}
	return ok
}

/* ── A and B: the marker is the whole decision ───────────────────────── */

func TestAMarkedDatabaseMayBeReset(t *testing.T) {
	dsn := newDatabase(t, "guard_marked", true)
	if !destructible(t, dsn) {
		t.Fatal("a database the harness marked was refused; the CI flow would not run")
	}
}

func TestAnUnmarkedDatabaseIsRefused(t *testing.T) {
	dsn := newDatabase(t, "guard_plain", false)
	if destructible(t, dsn) {
		t.Fatal("a database nobody marked was accepted for destruction")
	}
}

/* ── C: the refusal costs nothing ────────────────────────────────────── */

// The property that makes the guard worth having: a refused check leaves the
// database byte for byte as it found it. A guard that dropped a schema and
// then complained would be a slower way to lose the data.
func TestTheRefusalHappensBeforeAnythingIsTouched(t *testing.T) {
	dsn := newDatabase(t, "guard_untouched", false)
	exec(t, dsn, `CREATE SCHEMA chat`)
	exec(t, dsn, `CREATE TABLE chat.providers (api_key_cipher BYTEA NOT NULL)`)
	exec(t, dsn, `INSERT INTO chat.providers (api_key_cipher) VALUES ('\x00'::bytea)`)

	if destructible(t, dsn) {
		t.Fatal("the guard permitted a database it should have refused")
	}

	// Everything still there, including the schema a reset would have
	// dropped first.
	if got := scalar[int64](t, dsn, `SELECT count(*) FROM chat.providers`); got != 1 {
		t.Fatalf("the row count is %d after a refusal; the guard is not read-only", got)
	}
	if scalar[bool](t, dsn, `SELECT to_regclass('chat.providers') IS NULL`) {
		t.Fatal("the table is gone after a refusal")
	}
}

/* ── D: the exact accident that happened ─────────────────────────────── */

// TEST_POSTGRES_DSN pointing at the development database is a mistake
// anybody can make in one keystroke. It must not be enough to destroy it.
func TestPointingTheSuiteAtADevelopmentDatabaseIsNotEnoughToDestroyIt(t *testing.T) {
	dev := newDatabase(t, "guard_pretend_dev", false)
	// Shaped like the real thing: the schemas a reset would drop, with a row
	// standing in for the credential that was actually lost.
	exec(t, dev, `CREATE SCHEMA chat`)
	exec(t, dev, `CREATE SCHEMA github`)
	exec(t, dev, `CREATE TABLE chat.providers (name TEXT NOT NULL)`)
	exec(t, dev, `INSERT INTO chat.providers (name) VALUES ('LiteLLM real')`)

	t.Setenv("TEST_POSTGRES_DSN", dev)

	if destructible(t, os.Getenv("TEST_POSTGRES_DSN")) {
		t.Fatal("the development database was declared destructible because a " +
			"variable pointed at it; this is the incident, reproduced")
	}
	if got := scalar[string](t, dev, `SELECT name FROM chat.providers`); got != "LiteLLM real" {
		t.Fatalf("the provider row reads %q", got)
	}
}

/* ── E: the verdict is about the database, not the string ────────────── */

func TestTheHostSpellingDoesNotChangeTheVerdict(t *testing.T) {
	// The same database, addressed two ways. Every DSN-comparison scheme
	// this design rejected fails exactly here.
	marked := newDatabase(t, "guard_hosts", true)
	other := strings.Replace(marked, "127.0.0.1", "localhost", 1)
	if other == marked {
		other = strings.Replace(marked, "localhost", "127.0.0.1", 1)
	}
	if other == marked {
		t.Skip("the suite DSN names its host in a form this case cannot vary")
	}

	if destructible(t, marked) != destructible(t, other) {
		t.Fatalf("the same database got two answers: %q and %q", marked, other)
	}
}

/* ── F and G: what is deliberately NOT the rule ──────────────────────── */

// A name is chosen by whoever creates the database and proves nothing about
// what is inside it.
func TestANameEndingInTestIsNotAPermission(t *testing.T) {
	dsn := newDatabase(t, "corsi_looks_like_a_test", false)
	if !strings.Contains(dsn, "_test") {
		t.Fatalf("this case needs the name to contain _test: %s", dsn)
	}
	if destructible(t, dsn) {
		t.Fatal("a database was accepted because of its name")
	}
}

// An environment variable is a property of the shell, and the shell is what
// was wrong in the incident. Nothing in the environment may override the
// database's own answer.
func TestNoEnvironmentVariableCanOverrideTheDatabase(t *testing.T) {
	dsn := newDatabase(t, "guard_env", false)
	for _, name := range []string{
		"ALLOW_DESTRUCTIVE_TESTS", "CORSI_ALLOW_DESTRUCTIVE", "FORCE",
		"CI", "TEST_POSTGRES_ALLOW_RESET",
	} {
		t.Setenv(name, "1")
	}
	if destructible(t, dsn) {
		t.Fatal("an environment variable made an unmarked database destructible")
	}
}

/* ── H: the ordinary path still works ────────────────────────────────── */

// The database this suite is running against is the one the harness
// provisioned and marked. If this fails, `make ci` cannot run at all.
func TestTheHarnessProvisionedDatabaseIsMarked(t *testing.T) {
	if !destructible(t, adminDSN(t)) {
		t.Fatal("the suite's own database carries no marker: either the harness " +
			"stopped writing it, or these tests are running somewhere unexpected")
	}
}

// Marking is idempotent. The harness runs it on every invocation and a
// second run must not fail the gate.
func TestMarkingTwiceIsNotAnError(t *testing.T) {
	dsn := newDatabase(t, "guard_twice", true)
	if err := Mark(context.Background(), dsn); err != nil {
		t.Fatalf("second Mark: %v", err)
	}
	if !destructible(t, dsn) {
		t.Fatal("marking twice unmarked it")
	}
}

// recorder stands in for *testing.T so the refusal can be observed instead
// of suffered. It satisfies Fataler and nothing more, which is the same
// contract the real call site has.
type recorder struct {
	failed  bool
	message string
}

func (r *recorder) Helper() {}
func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
}

// A refusal has to FAIL the run, not skip it.
//
// The dangerous variant of this guard is the polite one: a suite aimed at
// the wrong database that quietly skips, reports ok, and lets somebody
// believe their integration tests passed. Nothing destroyed, nothing
// tested — the worst outcome, wearing the appearance of the best.
//
// The Fataler parameter makes the skip a compile error rather than a
// behaviour; this asserts the other half, that the refusal is not silent.
func TestARefusalStopsTheRunAndSaysWhy(t *testing.T) {
	dsn := newDatabase(t, "guard_must_fail", false)

	var rec recorder
	AssertDestructible(&rec, dsn)

	if !rec.failed {
		t.Fatal("refusing an unmarked database did not stop the test; a misaimed " +
			"run would report success having tested nothing")
	}
	// The message has to be actionable. Somebody reads it once, at the worst
	// possible moment, and it has to tell them what to do next.
	for _, must := range []string{"REFUSING", "Nothing has been changed", "test-integration-isolated", MarkerTable} {
		if !strings.Contains(rec.message, must) {
			t.Fatalf("the refusal does not mention %q:\n%s", must, rec.message)
		}
	}
}

// And a marked database is passed through in silence.
func TestAMarkedDatabaseIsNotStopped(t *testing.T) {
	var rec recorder
	AssertDestructible(&rec, newDatabase(t, "guard_pass", true))
	if rec.failed {
		t.Fatalf("a marked database was refused: %s", rec.message)
	}
}

// The marker has to outlive the resets it authorizes.
//
// It lives in `public` for exactly this reason: the suites drop named
// schemas, and a marker inside one of them would be destroyed by the first
// reset it permitted. Every run after that would refuse, and the fix
// somebody reached for would be to weaken the guard.
func TestTheMarkerSurvivesTheResetItAuthorizes(t *testing.T) {
	dsn := newDatabase(t, "guard_survives", true)
	exec(t, dsn, `CREATE SCHEMA chat`)
	exec(t, dsn, `CREATE SCHEMA releases`)

	// What a reset does.
	exec(t, dsn, `DROP SCHEMA IF EXISTS chat CASCADE`)
	exec(t, dsn, `DROP SCHEMA IF EXISTS releases CASCADE`)

	if !destructible(t, dsn) {
		t.Fatal("the marker did not survive a reset; the second run of the suite " +
			"would refuse a database the harness had provisioned")
	}
}

// An unreachable database is not a protected one. The two are different
// facts and the caller has to be able to tell them apart, or a typo in the
// port reads as "carefully guarded".
func TestAnUnreachableDatabaseIsAnErrorAndNotAQuietNo(t *testing.T) {
	_, err := Destructible(context.Background(),
		"postgres://nobody:nobody@127.0.0.1:1/does_not_exist?sslmode=disable")
	if err == nil {
		t.Fatal("an unreachable database answered instead of failing")
	}
}
