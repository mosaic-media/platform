package contracts

// StorageAdapter is the driven port through which the Platform obtains its
// transaction boundary (MEG-015 §03, [MAD-001]). An adapter — the built-in
// PostgreSQL module today, a future SQLite module tomorrow — provides the
// UnitOfWork and is responsible for binding each store resolved via Store to
// the live transaction. Making storage a port rather than a privileged
// implementation is what lets the backing engine be swapped without changing
// a single application-service call site: services depend on UnitOfWork, Tx
// and Store, never on a concrete engine.
//
// Only the UnitOfWork is exposed here. Binding resolved stores to the live
// transaction is an internal responsibility of the adapter's own Tx
// implementation, observable through Store, not a method callers invoke.
//
// [MAD-001]: mosaic-architecture docs/engineering/architecture/mad-001-transactional-store-extensibility
type StorageAdapter interface {
	// UnitOfWork returns the transaction boundary application services use to
	// coordinate writes across stores. Every store resolved via Store within
	// the returned UnitOfWork's Tx participates in the same underlying
	// transaction, so state and outbox events commit atomically.
	UnitOfWork() UnitOfWork
}
