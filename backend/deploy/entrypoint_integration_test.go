//go:build integration

// Package deploy holds the container entrypoint and its regression tests.
//
// The test in this file exercises the real `entrypoint.sh` against a
// throwaway database. It exists because a real production outage lived in
// the shell script,
// not in the migrations: every migration file was correct and every Go test
// passed while the chat schema was simply never created in production.
// A test that only reads the script for a matching line would not have
// caught it either, so this one runs the script and inspects the database
// it produced.
//
//	Run with:  TEST_POSTGRES_DSN=postgres://corsi:corsi@localhost:5432/postgres?sslmode=disable \
//	           go test -tags=integration ./deploy/
package deploy

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// adminDSN is the connection used to create and drop the throwaway database.
// Skips rather than fails when unset, matching the finance integration suite.
func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping entrypoint integration test")
	}
	return v
}

// withDatabase rewrites the database name in a postgres DSN.
func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// freshDatabase creates an empty database and returns its DSN. The database
// is dropped on cleanup. A dedicated database — rather than dropping schemas
// in the shared one — keeps this test from racing the finance suite, which
// `go test ./...` runs concurrently in another package.
func freshDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin := adminDSN(t)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}

	// Unique per run so a leaked database from an aborted run never collides.
	name := fmt.Sprintf("corsi_entrypoint_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = conn.Close(ctx)
		t.Skipf("cannot create a throwaway database (needs CREATEDB): %v", err)
	}
	_ = conn.Close(ctx)

	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Logf("cleanup: connect: %v", err)
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		// WITH (FORCE) terminates leftover connections so the drop cannot
		// hang on a pool the test failed to close.
		if _, err := c.Exec(context.Background(),
			`DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Logf("cleanup: drop database %s: %v", name, err)
		}
	})

	dsn, err := withDatabase(admin, name)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	return dsn
}

// entrypointRun is one execution of the real deploy/entrypoint.sh.
type entrypointRun struct {
	exitCode int
	output   string
	// serverStarted reports whether the script reached its final `exec`.
	// The fake server binary records this by creating a sentinel file, which
	// is how the test proves that a failed migration aborts the start.
	serverStarted bool
}

// harness builds the migrate binary once and provides a fake server binary,
// so the script under test runs unmodified against local paths.
type harness struct {
	migrateBin    string
	migrationsDir string
	serverBin     string
	sentinel      string
	script        string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()

	migrateBin := filepath.Join(dir, "migrate")
	build := exec.Command("go", "build", "-o", migrateBin, "./cmd/migrate")
	build.Dir = ".." // module root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build migrate binary: %v\n%s", err, out)
	}

	// Stands in for /app/corsi. Starting the real server would hold a port
	// and block; all this test needs to know is whether the exec happened.
	sentinel := filepath.Join(dir, "server-started")
	serverBin := filepath.Join(dir, "fake-corsi")
	script := "#!/bin/sh\ntouch " + sentinel + "\n"
	if err := os.WriteFile(serverBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake server: %v", err)
	}

	return &harness{
		migrateBin:    migrateBin,
		migrationsDir: filepath.Join("..", "migrations"),
		serverBin:     serverBin,
		sentinel:      sentinel,
		script:        "entrypoint.sh",
	}
}

func (h *harness) run(t *testing.T, dsn string) entrypointRun {
	t.Helper()
	_ = os.Remove(h.sentinel)

	cmd := exec.Command("sh", h.script)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"POSTGRES_DSN=" + dsn,
		"RUN_MIGRATIONS=true",
		"MIGRATE_BIN=" + h.migrateBin,
		"MIGRATIONS_DIR=" + h.migrationsDir,
		"SERVER_BIN=" + h.serverBin,
	}
	out, err := cmd.CombinedOutput()

	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run entrypoint: %v\n%s", err, out)
		}
	}
	_, statErr := os.Stat(h.sentinel)
	return entrypointRun{exitCode: code, output: string(out), serverStarted: statErr == nil}
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func connect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func relationExists(t *testing.T, conn *pgx.Conn, rel string) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(context.Background(),
		`SELECT to_regclass($1) IS NOT NULL`, rel).Scan(&exists); err != nil {
		t.Fatalf("to_regclass(%s): %v", rel, err)
	}
	return exists
}

func schemaExists(t *testing.T, conn *pgx.Conn, name string) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("pg_namespace(%s): %v", name, err)
	}
	return exists
}

// migrationVersion reads one module's version table. Returns dirty so a
// half-applied timeline fails the test instead of passing quietly.
func migrationVersion(t *testing.T, conn *pgx.Conn, table string) (version int64, dirty bool) {
	t.Helper()
	q := fmt.Sprintf(`SELECT version, dirty FROM %s`, pgx.Identifier{table}.Sanitize())
	if err := conn.QueryRow(context.Background(), q).Scan(&version, &dirty); err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	return version, dirty
}

// TestEntrypointMigratesEveryBoundedContext is that regression.
//
// It asserts the property the production deploy path must hold: running the
// entrypoint against an empty database leaves BOTH bounded contexts usable,
// each on its own migration timeline.
func TestEntrypointMigratesEveryBoundedContext(t *testing.T) {
	dsn := freshDatabase(t)
	h := newHarness(t)

	// Sanity: the database really is empty before the entrypoint runs. If
	// this fails the rest of the test proves nothing.
	pre := connect(t, dsn)
	if schemaExists(t, pre, "finance") || schemaExists(t, pre, "chat") {
		t.Fatalf("precondition: database is not empty")
	}
	_ = pre.Close(context.Background())

	run := h.run(t, dsn)
	if run.exitCode != 0 {
		t.Fatalf("entrypoint exited %d\n%s", run.exitCode, run.output)
	}
	if !run.serverStarted {
		t.Errorf("entrypoint did not reach the server exec\n%s", run.output)
	}

	conn := connect(t, dsn)

	// --- every schema exists ------------------------------------------
	//
	// One entry per timeline the entrypoint applies. Adding a module means
	// adding it here, and forgetting to reproduces that outage: the migration
	// files are all correct and the schema is simply never created.
	for _, s := range []string{"finance", "chat", "releases", "github"} {
		if !schemaExists(t, conn, s) {
			t.Errorf("schema %q does not exist after the entrypoint migration stage", s)
		}
	}

	// --- finance did not regress --------------------------------------
	for _, rel := range []string{
		"finance.categories",
		"finance.cards",
		"finance.transactions",
		"finance.persons",
		"finance.purchase_plans",
	} {
		if !relationExists(t, conn, rel) {
			t.Errorf("relation %q missing", rel)
		}
	}

	// --- chat is present ----------------------------------------------
	for _, rel := range []string{
		"chat.providers",
		"chat.agents",
		"chat.conversations",
		"chat.messages",
	} {
		if !relationExists(t, conn, rel) {
			t.Errorf("relation %q missing", rel)
		}
	}

	// --- job radar is present -----------------------------------------
	for _, rel := range []string{
		"jobradar.companies",
		"jobradar.opportunities",
		"jobradar.stage_events",
	} {
		if !relationExists(t, conn, rel) {
			t.Errorf("relation %q missing", rel)
		}
	}

	// --- threads is present -------------------------------------------
	if !relationExists(t, conn, "threads.threads") {
		t.Errorf("relation %q missing", "threads.threads")
	}

	// --- the meta threads integration is present ----------------------
	// A DIFFERENT schema from `threads` above, on purpose: one is our
	// content pipeline, the other is a credential for Meta's network.
	if !relationExists(t, conn, "meta_threads.connections") {
		t.Errorf("relation %q missing", "meta_threads.connections")
	}

	// --- palace is present --------------------------------------------
	// `palace.memories` and `palace.sources` are the OPERATOR's knowledge
	// and the evidence behind it. They are not `chat.memories` and
	// `chat.agent_sources`, which belong to an agent. Both pairs are
	// asserted here so a migration that created the wrong one is a
	// failure rather than a surprise later.
	for _, rel := range []string{
		"palace.rooms",
		"palace.artifacts",
		"palace.artifact_items",
		"palace.sources",
		"palace.memories",
		"palace.memory_sources",
		"palace.relations",
		"palace.sessions",
	} {
		if !relationExists(t, conn, rel) {
			t.Errorf("relation %q missing", rel)
		}
	}

	// --- bookkeeping is independent -----------------------------------
	// This is the assertion that pins the `-table` flag. Migrating chat
	// without it would leave `schema_migrations_chat` absent and silently
	// consume finance's timeline.
	if !relationExists(t, conn, "public.schema_migrations") {
		t.Fatalf("finance version table `schema_migrations` missing")
	}
	if !relationExists(t, conn, "public.schema_migrations_chat") {
		t.Fatalf("chat version table `schema_migrations_chat` missing — chat was migrated without -table")
	}
	// Same assertion for every timeline added since. A module migrated
	// without `-table` reads finance's fully applied version table,
	// concludes there is nothing to do, and creates no schema at all — the
	// exact failure mode the two lines above exist for.
	for _, table := range []string{
		"public.schema_migrations_releases",
		"public.schema_migrations_github",
		"public.schema_migrations_jobradar",
		"public.schema_migrations_threads",
		"public.schema_migrations_metathreads",
		"public.schema_migrations_palace",
	} {
		if !relationExists(t, conn, table) {
			t.Fatalf("version table `%s` missing — that module was migrated without -table", table)
		}
	}

	finV, finDirty := migrationVersion(t, conn, "schema_migrations")
	chatV, chatDirty := migrationVersion(t, conn, "schema_migrations_chat")
	if finDirty {
		t.Errorf("finance timeline is dirty at version %d", finV)
	}
	if chatDirty {
		t.Errorf("chat timeline is dirty at version %d", chatV)
	}
	if finV <= 0 {
		t.Errorf("finance version = %d, want > 0", finV)
	}
	if chatV <= 0 {
		t.Errorf("chat version = %d, want > 0", chatV)
	}
}

// TestEntrypointIsIdempotent covers the restart path: a container that
// reboots against an already-migrated database must come up cleanly.
func TestEntrypointIsIdempotent(t *testing.T) {
	dsn := freshDatabase(t)
	h := newHarness(t)

	if run := h.run(t, dsn); run.exitCode != 0 {
		t.Fatalf("first run exited %d\n%s", run.exitCode, run.output)
	}
	conn := connect(t, dsn)
	finV1, _ := migrationVersion(t, conn, "schema_migrations")
	chatV1, _ := migrationVersion(t, conn, "schema_migrations_chat")
	_ = conn.Close(context.Background())

	run := h.run(t, dsn)
	if run.exitCode != 0 {
		t.Fatalf("second run exited %d\n%s", run.exitCode, run.output)
	}
	if !run.serverStarted {
		t.Errorf("second run did not reach the server exec\n%s", run.output)
	}

	conn2 := connect(t, dsn)
	finV2, finDirty := migrationVersion(t, conn2, "schema_migrations")
	chatV2, chatDirty := migrationVersion(t, conn2, "schema_migrations_chat")
	if finV1 != finV2 || chatV1 != chatV2 {
		t.Errorf("versions moved on a no-op run: finance %d→%d, chat %d→%d",
			finV1, finV2, chatV1, chatV2)
	}
	if finDirty || chatDirty {
		t.Errorf("a timeline went dirty on the second run")
	}
}

// TestEntrypointAbortsWhenMigrationFails covers the failure contract: a
// migration that cannot run must stop the container, not degrade it into
// serving a schema it does not have.
func TestEntrypointAbortsWhenMigrationFails(t *testing.T) {
	adminDSN(t) // skip consistently when the suite is not configured
	h := newHarness(t)

	// Points at a database that does not exist, so the very first migration
	// invocation fails.
	bad, err := withDatabase("postgres://corsi:corsi@127.0.0.1:1/none?sslmode=disable", "definitely_absent")
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}

	run := h.run(t, bad)
	if run.exitCode == 0 {
		t.Errorf("entrypoint exited 0 on a failed migration\n%s", run.output)
	}
	if run.serverStarted {
		t.Errorf("entrypoint started the server despite a failed migration\n%s", run.output)
	}
	if !strings.Contains(run.output, "finance migrations failed") {
		t.Errorf("failure output does not name the module that failed:\n%s", run.output)
	}
}
