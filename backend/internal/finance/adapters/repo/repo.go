// Package repo is the driven adapter that backs the finance ports with
// Postgres. Every repository routes its query through postgres.Conn(ctx, pool)
// so it transparently participates in any TxManager.WithinTx scope.
package repo

import "github.com/jackc/pgx/v5/pgxpool"

// Repositories bundles every concrete repository the finance module needs.
// Add new fields here when introducing a new aggregate.
type Repositories struct {
	Categories    *CategoryRepo
	Transactions  *TransactionRepo
	Cards         *CardRepo
	Persons       *PersonRepo
	PurchasePlans *PurchasePlanRepo
	// Clock is the database's own clock. It is a repository because the
	// finance contract classifies against `now()` in SQL, and a second
	// clock is a second answer — see clock.go.
	Clock *ClockRepo
	// RecurringEntries are money that REPEATS, in either direction — see
	// domain/recurringentry.go for why they never become transactions.
	RecurringEntries *RecurringEntryRepo
	// ImportBatches is the statement-import staging area. Nothing it holds
	// is visible to any financial read until a commit materialises it.
	ImportBatches *ImportBatchRepo
	// ImportSources issues the identity a statement is imported under, so
	// no caller composes the dedup namespace. See domain.ImportSource.
	ImportSources *ImportSourceRepo
}

func New(pool *pgxpool.Pool) *Repositories {
	return &Repositories{
		Categories:       NewCategoryRepo(pool),
		Transactions:     NewTransactionRepo(pool),
		Cards:            NewCardRepo(pool),
		Persons:          NewPersonRepo(pool),
		PurchasePlans:    NewPurchasePlanRepo(pool),
		Clock:            NewClockRepo(pool),
		RecurringEntries: NewRecurringEntryRepo(pool),
		ImportBatches:    NewImportBatchRepo(pool),
		ImportSources:    NewImportSourceRepo(pool),
	}
}
