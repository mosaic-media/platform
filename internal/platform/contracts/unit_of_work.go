package contracts

import "context"

// UnitOfWork is the transaction boundary application services use to
// coordinate writes across multiple stores (MEG-015 §03).
type UnitOfWork interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Tx provides transaction-scoped access to Platform stores. Every store
// reached through a single Tx participates in the same underlying
// transaction, so state and outbox events commit atomically.
type Tx interface {
	Users() UserStore
	Sessions() SessionStore
	Permissions() PermissionStore
	Config() ConfigStore
	Outbox() EventOutbox
}
