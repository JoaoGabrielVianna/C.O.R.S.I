// Package app holds application services — the orchestrators that drive
// domain logic and coordinate outbound ports inside a unit-of-work.
//
// Services depend on ports (interfaces) only, not adapters. They are the
// only layer allowed to open a transaction via TxManager.WithinTx; domain
// objects know nothing about transactions, HTTP, or SQL.
package app

import (
	"log/slog"

	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/platform/postgres"
)

type Service struct {
	repos *repo.Repositories
	txm   *postgres.TxManager
	log   *slog.Logger
}

func NewService(repos *repo.Repositories, txm *postgres.TxManager, log *slog.Logger) *Service {
	return &Service{repos: repos, txm: txm, log: log}
}
