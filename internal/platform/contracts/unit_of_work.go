// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import "context"

// UnitOfWork is the transaction boundary application services use to
// coordinate writes across multiple stores.
type UnitOfWork interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Tx provides transaction-scoped access to Platform stores. Every store
// reached through a single Tx participates in the same underlying
// transaction, so state and outbox events commit atomically.
// The set of stores is Platform-owned and closed, and this interface
// enumerates it. Capabilities do not own stores (ADR 0012), so there is no
// registration mechanism and nothing to resolve at runtime; growing this
// set is deliberate Platform evolution, which is why it looks like an edit
// to a Platform interface rather than a plugin point.
//
// One transaction spans one bounded context's stores plus the outbox
// (ADR 0014). Work that crosses contexts is two transactions joined by an
// event, not one transaction touching both.
type Tx interface {
	Users() UserStore
	Sessions() SessionStore
	Permissions() PermissionStore
	Config() ConfigStore
	Outbox() EventOutbox
	Credentials() CredentialStore

	// The content model (ADR 0013) — the first stores added to this set
	// since it was closed.
	Nodes() NodeStore
	Parts() PartStore
	Relations() RelationStore
	SourceBindings() SourceBindingStore

	// ModuleSettings persists an optional module's user-managed settings
	// document (ADR 0021). It joins the set so a settings change and its
	// outbox event commit in one transaction, like every other write.
	ModuleSettings() ModuleSettingsStore

	// UserPreferences persists what a user chose for themselves — expert mode
	// (ADR 0058) first, and more to come. It joins the set for the same reason
	// ModuleSettings did: a preference change emits an outbox event, and the
	// two must commit together or neither.
	UserPreferences() UserPreferenceStore
}
