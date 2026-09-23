// Package app holds application services — the orchestrators that drive
// domain logic and coordinate outbound ports inside a unit-of-work.
//
// Services depend on ports (interfaces) only, not adapters. They are the
// only layer allowed to open a transaction via TxManager.WithinTx; domain
// objects know nothing about transactions, HTTP, or SQL.
package app

import (
	"log/slog"
	"time"

	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/platform/postgres"
)

type Service struct {
	repos *repo.Repositories
	txm   *postgres.TxManager
	log   *slog.Logger
	// loc is the reporting zone, FINANCE_TIMEZONE, and it is the ONLY
	// place a month boundary is decided.
	//
	// ── Why the service holds it rather than each caller passing one ───
	// Because "which month is this instant in" must have one answer across
	// the product. An instant at 21:00 on 31 August is August in São Paulo
	// and September in UTC, so a screen and a capability passing different
	// zones would materialise the same obligation into two different
	// months — and the unique index would happily hold both, because they
	// really are different months.
	//
	// The composition root reads the zone once and hands it here. Nil
	// falls back to UTC rather than to the process's local zone, for the
	// reason finance.Deps gives: a wrong answer that is obviously wrong
	// beats one that depends on which machine the binary runs on.
	loc *time.Location
}

func NewService(repos *repo.Repositories, txm *postgres.TxManager, log *slog.Logger, loc *time.Location) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{repos: repos, txm: txm, log: log, loc: loc}
}

// ReportingLocation is the zone this service cuts months in. Exposed so an
// adapter can label an answer with it rather than guessing.
func (s *Service) ReportingLocation() *time.Location { return s.loc }
