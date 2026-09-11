package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// migrateDSN rewrites a standard postgres:// DSN to the pgx5:// scheme that
// the golang-migrate pgx/v5 driver registers under.
func migrateDSN(d string) string {
	switch {
	case strings.HasPrefix(d, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(d, "postgres://")
	case strings.HasPrefix(d, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(d, "postgresql://")
	}
	return d
}

// withMigrationsTable points the driver at a per-module version table.
//
// Each bounded context owns an independent migration timeline, so they
// cannot share one version table — chat starting at 0 would otherwise read
// as "finance is at 8, replay nothing" or vice versa.
//
// An empty table name leaves the driver's default (`schema_migrations`)
// alone. That default belongs to finance, which predates the split: moving
// it now would make golang-migrate see a fully populated database as
// version 0 and try to replay every migration against existing tables.
func withMigrationsTable(dsn, table string) (string, error) {
	if table == "" {
		return dsn, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func main() {
	dsn := flag.String("dsn", os.Getenv("POSTGRES_DSN"), "postgres dsn")
	dir := flag.String("dir", "migrations/finance", "migrations dir")
	table := flag.String("table", "", "migrations version table (empty = driver default)")
	step := flag.Int("step", 0, "step count for up/down (0 = all)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("usage: migrate -dsn=... -dir=... [-table=NAME] [-step=N] <up|down|version|force>")
		os.Exit(1)
	}
	if *dsn == "" {
		fmt.Println("missing -dsn or POSTGRES_DSN")
		os.Exit(1)
	}

	target, err := withMigrationsTable(migrateDSN(*dsn), *table)
	if err != nil {
		slog.Error("build dsn", "err", err)
		os.Exit(1)
	}

	m, err := migrate.New("file://"+*dir, target)
	if err != nil {
		slog.Error("init migrate", "err", err)
		os.Exit(1)
	}
	defer m.Close()

	cmd := args[0]
	switch cmd {
	case "up":
		if *step > 0 {
			err = m.Steps(*step)
		} else {
			err = m.Up()
		}
	case "down":
		if *step > 0 {
			err = m.Steps(-*step)
		} else {
			err = m.Down()
		}
	case "version":
		v, dirty, vErr := m.Version()
		if vErr != nil && !errors.Is(vErr, migrate.ErrNilVersion) {
			slog.Error("version", "err", vErr)
			os.Exit(1)
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return
	case "force":
		if len(args) < 2 {
			fmt.Println("usage: ... force <version>")
			os.Exit(1)
		}
		v, perr := strconv.Atoi(args[1])
		if perr != nil {
			slog.Error("parse version", "err", perr)
			os.Exit(1)
		}
		err = m.Force(v)
	default:
		fmt.Printf("unknown command: %s\n", cmd)
		os.Exit(1)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		slog.Error("migrate", "cmd", cmd, "err", err)
		os.Exit(1)
	}
	slog.Info("migrate ok", "cmd", cmd)
}
