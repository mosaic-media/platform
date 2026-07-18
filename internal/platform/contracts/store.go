package contracts

import "fmt"

// Store resolves the store contract of type T bound to the transaction scope
// tx. It is the uniform, type-safe way every application service and, later,
// every capability obtains a store within a transaction (MEG-015 §03,
// [MAD-001]): a command that would have called tx.Users() obtains the same
// contract with Store[UserStore](tx), and the outbox with
// Store[EventOutbox](tx) — fully typed, with no assertion at the call site.
//
// Store is a package-level function rather than a Tx method because Go
// methods cannot take type parameters. Resolution is driven by an internal,
// unexported mapping from store type to the transaction-bound store instance
// (resolveStore). Nothing at this caller-facing boundary requires a store to
// register or "ask permission" to join the transaction — that access-gate
// property is exactly what MAD-001 §03 rejected the any-keyed extension
// registry (option a) for losing, and what the typed accessor (option b/d)
// preserves.
//
// For a type T that no adapter binds to the transaction scope, Store returns
// an Internal-category Platform error rather than a zero store, so a
// mis-typed call site fails loudly instead of silently handing back a nil
// contract.
//
// [MAD-001]: mosaic-architecture docs/engineering/architecture/mad-001-transactional-store-extensibility
func Store[T any](tx Tx) (T, error) {
	var zero T
	// A store contract T is an interface type, so var zero T is a nil
	// interface carrying no dynamic type — switching on any(zero) would match
	// nothing. (*T)(nil) instead carries the type *T as its dynamic type, which
	// is how resolveStore recovers which store was requested.
	resolved, ok := resolveStore(tx, (*T)(nil))
	if !ok {
		return zero, NewError(Internal,
			fmt.Sprintf("contracts: no store bound to the transaction scope for type %T", zero))
	}
	typed, ok := resolved.(T)
	if !ok {
		return zero, NewError(Internal,
			fmt.Sprintf("contracts: store bound for type %T has unexpected concrete type %T", zero, resolved))
	}
	return typed, nil
}

// resolveStore maps the requested store type to the exact store instance the
// transaction scope exposes for it. It is the internal, unexported resolution
// table Store delegates to; keeping it unexported means the fully typed Store
// signature stays the only way a caller reaches a store, per MAD-001 §03.
//
// It is keyed on the requested type carried as a typed nil pointer: typ is
// (*T)(nil), so its dynamic type is *UserStore, *EventOutbox, and so on.
// Matching on the pointer type recovers the requested store contract even
// though the store interfaces themselves have nil zero values.
//
// This slice resolves the six Core Platform stores by delegating to Tx's
// named accessors, which guarantees Store[T](tx) returns the identical
// instance the matching accessor would for the same tx — the equivalence the
// contracts-package test proves against a fake, since there is no real
// transaction here to prove atomicity against (that is a later slice's job).
// When Tx is sealed and its named accessors are removed (a later rework
// slice), this table is the single place that repoints at the StorageAdapter's
// live-transaction binding; Store's signature and every call site stay
// unchanged.
func resolveStore(tx Tx, typ any) (any, bool) {
	switch typ.(type) {
	case *UserStore:
		return tx.Users(), true
	case *SessionStore:
		return tx.Sessions(), true
	case *PermissionStore:
		return tx.Permissions(), true
	case *ConfigStore:
		return tx.Config(), true
	case *EventOutbox:
		return tx.Outbox(), true
	case *CredentialStore:
		return tx.Credentials(), true
	default:
		return nil, false
	}
}
