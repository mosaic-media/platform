package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// StorageAdapter is the PostgreSQL implementation of contracts.StorageAdapter
// (MAD-001, MEG-015 §03 — "Storage itself is a port... the built-in
// PostgreSQL adapter can be replaced... without changing a call site"). It is
// the driven port through which the Platform obtains its transaction
// boundary. Reaching the UnitOfWork through this port, rather than
// constructing a concrete *postgres.UnitOfWork at each call site, is what
// makes the backing engine swappable: a future SQLite module that implements
// the same port drops in without changing a single application-service call
// site, because services depend on contracts.UnitOfWork / contracts.Tx /
// contracts.Store, never on this package.
//
// Every store resolved via contracts.Store within a Tx handed out by the
// returned UnitOfWork is bound to that transaction's one underlying handle, so
// a state write and its outbox event commit atomically (MEG-015 §11 — Outbox
// gate). Binding is the adapter's internal responsibility (see the tx struct
// in unit_of_work.go), observable through Store, not a method callers invoke.
type StorageAdapter struct {
	uow *UnitOfWork
}

// NewStorageAdapter builds the PostgreSQL StorageAdapter over pool.
func NewStorageAdapter(pool *pgxpool.Pool) *StorageAdapter {
	return &StorageAdapter{uow: NewUnitOfWork(pool)}
}

// UnitOfWork returns the transaction boundary application services use to
// coordinate writes across stores.
func (a *StorageAdapter) UnitOfWork() contracts.UnitOfWork {
	return a.uow
}

// Compile-time assertion that the PostgreSQL adapter satisfies the Platform
// storage port. This is what a second engine (e.g. SQLite) would also satisfy.
var _ contracts.StorageAdapter = (*StorageAdapter)(nil)
